package managerapp

import (
	"CrackHash/internal/common"
	managerapi "CrackHash/internal/manager-api"
	"context"
	"errors"

	"github.com/google/uuid"
	lru "github.com/hashicorp/golang-lru"
	"go.uber.org/zap"
)

type crackHashRequest struct {
	ctx        context.Context
	taskId     TaskIdent
	chToPutRes chan CrackHashResponse
}

type CrackHashResponse struct {
	reqId string
}

type getRequestStatusRequest struct {
	ctx        context.Context
	reqId      string
	chToPutRes chan GetRequestStatusResponse
}

const (
	REQUEST_STATUS_READY = iota
	REQUEST_STATUS_IN_PROGRESS
	REQUEST_STATUS_TOO_HARD
	REQUEST_STATUS_UNKNOWN
)

type GetRequestStatusResponse struct {
	Status int
	Data   []string
}

type taskContext struct {
	ctx          context.Context
	cancelTask   context.CancelFunc
	reqs         []string
	informResult chan managerapi.InformCrackHashResultBody
}

type HashTasksManager struct {
	cfg    *ManagerConfig
	logger *zap.Logger

	pendingTasks    map[TaskIdent]*taskContext
	pendingRequests map[string]TaskIdent

	doneRequests *lru.ARCCache
	doneTasks    *lru.ARCCache

	wr *WorkersRegistry

	ae *common.ActorExecutor
}

func NewHashTasksManager(cfg *ManagerConfig, wr *WorkersRegistry) (*HashTasksManager, error) {
	logger, err := zap.NewProduction()
	if err != nil {
		return nil, err
	}

	cacheReq, err := lru.NewARC(int(cfg.CacheSize))
	if err != nil {
		panic(err)
	}

	cacheTasks, err := lru.NewARC(int(cfg.CacheSize))
	if err != nil {
		panic(err)
	}

	logger = logger.With(zap.String("actor", "HashTaskManager"))

	htm := &HashTasksManager{
		cfg:             cfg,
		logger:          logger,
		pendingTasks:    make(map[TaskIdent]*taskContext),
		pendingRequests: make(map[string]TaskIdent),

		doneRequests: cacheReq,
		doneTasks:    cacheTasks,

		wr: wr,

		ae: nil,
	}

	htm.ae = common.NewActorExecutor(htm)
	htm.ae.Start()
	return htm, err
}

func (htm *HashTasksManager) processCrackHashReq(req *crackHashRequest) {
	reqId := uuid.NewString()
	defer func() {
		req.chToPutRes <- CrackHashResponse{reqId: reqId}
	}()

	htm.logger.Info(
		"new crack hash request",
		zap.String("hash", req.taskId.hash),
		zap.Uint("maxWorldLength", req.taskId.worldLength),
	)

	_, ok := htm.doneTasks.Get(req.taskId)
	if ok {
		htm.doneRequests.Add(reqId, req.taskId)
		return
	}

	htm.pendingRequests[reqId] = req.taskId

	task, ok := htm.pendingTasks[req.taskId]
	if ok {
		task.reqs = append(task.reqs, reqId)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), htm.cfg.CrackHashTimeout)

	resChan, informResult, err := NewWorkersProxy(ctx, htm.cfg, htm.wr, req.taskId)
	if err != nil {
		panic(err)
	}

	htm.pendingTasks[req.taskId] = &taskContext{
		ctx,
		cancel,
		[]string{reqId},
		informResult,
	}

	go func() {
		res := <-resChan
		htm.ae.SendReq(ctx, res)
	}()
}

func (htm *HashTasksManager) processCrackHashResult(res CrackHashResult) {
	htm.logger.Info(
		"task has completed",
		zap.String("hash", res.TaskId.hash),
		zap.Uint("maxWorldLength", res.TaskId.worldLength),
		zap.Error(res.Err),
	)

	tCtx, ok := htm.pendingTasks[res.TaskId]
	needToRememberRes := !(errors.Is(res.Err, ErrNoWorkersError) || errors.Is(res.Err, ErrCanceled))
	defer func() {
		if !needToRememberRes {
			return
		}

		var status int
		if errors.Is(res.Err, ErrCantCrack) {
			status = REQUEST_STATUS_TOO_HARD
		} else if res.Err != nil {
			panic("unknown error")
		} else {
			status = REQUEST_STATUS_READY
		}

		htm.doneTasks.Add(res.TaskId,
			GetRequestStatusResponse{
				Status: status,
				Data:   res.Data,
			})
	}()
	if !ok {
		return
	}

	for _, reqId := range tCtx.reqs {
		delete(htm.pendingRequests, reqId)
		if needToRememberRes {
			htm.doneRequests.Add(reqId, res.TaskId)
		}
	}

	delete(htm.pendingTasks, res.TaskId)
	tCtx.cancelTask()
}

func (htm *HashTasksManager) processGetStatusRequest(req getRequestStatusRequest) {
	replyUnknown := func() {
		select {
		case req.chToPutRes <- GetRequestStatusResponse{Status: REQUEST_STATUS_UNKNOWN}:
		case <-req.ctx.Done():
		}
	}

	replyRes := func(res GetRequestStatusResponse) {
		select {
		case req.chToPutRes <- res:
		case <-req.ctx.Done():
		}
	}

	taskId, ok := htm.doneRequests.Get(req.reqId)
	if ok {

		res, ok := htm.doneTasks.Get(taskId)
		if !ok {
			htm.doneRequests.Remove(req.reqId)
			replyUnknown()
			return
		}

		replyRes(res.(GetRequestStatusResponse))
		return
	}

	taskId, ok = htm.pendingRequests[req.reqId]
	if !ok {
		replyUnknown()
		return
	}

	replyRes(GetRequestStatusResponse{Status: REQUEST_STATUS_IN_PROGRESS})
}

func (htm *HashTasksManager) processInformResult(res managerapi.InformCrackHashResultBody) {
	htm.logger.Info(
		"informing about req to workers result",
		zap.String("reqId", res.RequestId),
		zap.String("resultType", res.ResultType),
	)
	for _, tCtx := range htm.pendingTasks {
		go func() {
			select {
			case tCtx.informResult <- res:
			case <-tCtx.ctx.Done():
			}
		}()
	}
}

func (htm *HashTasksManager) ProcessMessage(req any) {
	switch v := req.(type) {
	case crackHashRequest:
		if !common.Done(v.ctx) {
			htm.processCrackHashReq(&v)
		}
	case CrackHashResult:
		htm.processCrackHashResult(v)
	case getRequestStatusRequest:
		if !common.Done(v.ctx) {
			htm.processGetStatusRequest(v)
		}
	case managerapi.InformCrackHashResultBody:
		htm.processInformResult(v)
	default:
		panic("Unexpected event")
	}
}

func (htm *HashTasksManager) CrackHash(ctx context.Context, taskId TaskIdent) chan CrackHashResponse {
	chToPutRes := make(chan CrackHashResponse)
	htm.ae.SendReq(
		ctx,
		crackHashRequest{
			ctx:        ctx,
			taskId:     taskId,
			chToPutRes: chToPutRes,
		})
	return chToPutRes
}

func (htm *HashTasksManager) GetRequestStatus(ctx context.Context, reqId string) chan GetRequestStatusResponse {
	chToPutRes := make(chan GetRequestStatusResponse)
	htm.ae.SendReq(
		ctx,
		getRequestStatusRequest{
			ctx:        ctx,
			reqId:      reqId,
			chToPutRes: chToPutRes,
		})
	return chToPutRes
}

func (htm *HashTasksManager) InformResult(res managerapi.InformCrackHashResultBody) {
	go func() {
		htm.ae.SendReq(context.Background(), res)
	}()
}
