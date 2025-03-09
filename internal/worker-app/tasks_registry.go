package workerapp

import (
	"CrackHash/internal/common"
	"context"
	"fmt"

	"github.com/google/uuid"
	lru "github.com/hashicorp/golang-lru"
	"go.uber.org/zap"
)

const (
	REQUEST_STATUS_DONE = iota
	REQUEST_STATUS_IN_PROGRESS
	REQUEST_STATUS_UNKNOWN
)

type getReqStatus struct {
	ctx        context.Context
	chToPutRes chan reqStatus
	reqId      string
}

type reqStatus struct {
	status int
	res    CrackResult
}

type taskIdent struct {
	hash      string
	wordRange common.PartRange
}

type taskReq struct {
	ctx        context.Context
	hash       string
	wordRange  common.PartRange
	chReqId    chan string
	chToPutRes chan CrackResult
}

type cancelTaskReq struct {
	ctx        context.Context
	requestId  string
	chToPutRes chan struct{}
}

type requestContexts = map[string]chan CrackResult

type taskContext struct {
	cancel          context.CancelFunc
	requestsForTask requestContexts
}

type TasksRegistryActor struct {
	logger *zap.Logger

	cfg *WorkerConfig

	pendingTasks    map[taskIdent]*taskContext
	pendingRequests map[string]taskIdent

	doneRequests *lru.ARCCache
	doneTasks    *lru.ARCCache

	ae *common.ActorExecutor
}

func NewTaskRegistryActor(cfg *WorkerConfig) (*TasksRegistryActor, error) {

	if cfg == nil {
		return nil, fmt.Errorf("provide config")
	}
	logger, err := zap.NewProduction()
	if err != nil {
		return nil, err
	}

	cacheSize := cfg.CacheSize

	cacheReq, err := lru.NewARC(int(cacheSize))
	if err != nil {
		return nil, err
	}

	cacheTasks, err := lru.NewARC(int(cacheSize))
	if err != nil {
		return nil, err
	}

	logger = logger.With(zap.String("actor", "task-registry"))

	actor := &TasksRegistryActor{
		cfg:             cfg,
		logger:          logger,
		pendingTasks:    make(map[taskIdent]*taskContext),
		pendingRequests: make(map[string]taskIdent),

		doneRequests: cacheReq,
		doneTasks:    cacheTasks,

		ae: nil,
	}

	actor.ae = common.NewActorExecutor(actor)

	actor.ae.Start()

	return actor, nil
}

func (tr *TasksRegistryActor) CreateCrackHashTask(ctx context.Context, hash string,
	wordRange common.PartRange) (chan CrackResult, chan string) {
	chToPutRes := make(chan CrackResult)
	chToPutReqId := make(chan string)

	req := taskReq{
		hash:       hash,
		wordRange:  wordRange,
		ctx:        ctx,
		chReqId:    chToPutReqId,
		chToPutRes: chToPutRes,
	}

	ok := tr.ae.SendReq(ctx, req)

	if !ok {
		close(chToPutRes)
	}
	return chToPutRes, chToPutReqId
}

func (tr *TasksRegistryActor) CancelTask(ctx context.Context, reqId string) chan struct{} {
	chToPutRes := make(chan struct{})

	req := cancelTaskReq{
		ctx:        ctx,
		requestId:  reqId,
		chToPutRes: chToPutRes,
	}

	ok := tr.ae.SendReq(ctx, req)

	if !ok {
		close(chToPutRes)
	}
	return chToPutRes
}

func (tr *TasksRegistryActor) GetRequestStatus(ctx context.Context, reqId string) chan reqStatus {
	chToPutRes := make(chan reqStatus)
	req := getReqStatus{
		ctx:        ctx,
		reqId:      reqId,
		chToPutRes: chToPutRes,
	}

	ok := tr.ae.SendReq(ctx, req)

	if !ok {
		close(chToPutRes)
	}
	return chToPutRes
}

func (tr *TasksRegistryActor) processResult(res WorkDone) {
	taskId := taskIdent{
		res.Hash,
		res.Range,
	}
	task, ok := tr.pendingTasks[taskId]
	if !ok {
		return
	}

	tr.doneTasks.Add(taskId, res.Result)
	for reqId, chToPutRes := range task.requestsForTask {
		chToPutRes <- res.Result
		delete(tr.pendingRequests, reqId)
		tr.doneRequests.Add(reqId, taskId)
	}

	task.cancel()
	delete(tr.pendingTasks, taskId)
}

func (tr *TasksRegistryActor) processRequest(req taskReq) {
	requestId := uuid.NewString()
	select {
	case req.chReqId <- requestId:
	case <-req.ctx.Done():
		return
	}

	taskId := taskIdent{
		hash:      req.hash,
		wordRange: req.wordRange,
	}

	tr.logger.Info(
		"Received new crack hash request",
		zap.String("request-id", requestId),
		zap.String("hash", req.hash),
		zap.Uint("partNumber", req.wordRange.PartNumber),
		zap.Uint("partCount", req.wordRange.PartCount),
	)

	if v, ok := tr.doneTasks.Get(taskId); ok {
		tr.doneRequests.Add(requestId, taskId)
		req.chToPutRes <- v.(CrackResult)
		return
	}

	tr.pendingRequests[requestId] = taskId

	task, ok := tr.pendingTasks[taskId]
	if ok {
		task.requestsForTask[requestId] = req.chToPutRes
		return
	}

	newCtx, cancel := context.WithTimeout(context.Background(), tr.cfg.CrackHashTimeout)

	tr.pendingTasks[taskId] = &taskContext{
		cancel: cancel,
		requestsForTask: map[string]chan CrackResult{
			requestId: req.chToPutRes,
		},
	}

	tr.createNewWorker(newCtx, taskId.hash, taskId.wordRange)
}

func (tr *TasksRegistryActor) processCancelRequest(cancelReq cancelTaskReq) {
	tr.logger.Info(
		"Canceling request",
		zap.String("request-id", cancelReq.requestId),
	)

	taskId, ok := tr.pendingRequests[cancelReq.requestId]
	if !ok {
		cancelReq.chToPutRes <- struct{}{}
		return
	}

	task, ok := tr.pendingTasks[taskId]
	if !ok {
		panic(fmt.Errorf("no task for pending req: %s", cancelReq.requestId))
	}

	chToPutRes, ok := task.requestsForTask[cancelReq.requestId]
	if !ok {
		panic("NO REQ")
	}

	chToPutRes <- CrackResult{
		ResultType: RESULT_TYPE_CANCELED,
	}
	delete(task.requestsForTask, cancelReq.requestId)
	if len(task.requestsForTask) == 0 {
		tr.logger.Debug(
			"Canceling task",
			zap.String("hash", taskId.hash),
			zap.Uint("partNumber", taskId.wordRange.PartNumber),
			zap.Uint("partCount", taskId.wordRange.PartCount),
		)
		task.cancel()
		delete(tr.pendingTasks, taskId)
	}

	delete(tr.pendingRequests, cancelReq.requestId)

	cancelReq.chToPutRes <- struct{}{}
}

func (tr *TasksRegistryActor) processGetStatusRequest(req getReqStatus) {
	_, ok := tr.pendingRequests[req.reqId]

	if ok {
		req.chToPutRes <- reqStatus{
			status: REQUEST_STATUS_IN_PROGRESS,
		}
		return
	}

	taskIdRaw, ok := tr.doneRequests.Get(req.reqId)

	if !ok {
		req.chToPutRes <- reqStatus{
			status: REQUEST_STATUS_UNKNOWN,
		}

		return
	}

	taskId := taskIdRaw.(taskIdent)

	resRaw, ok := tr.doneTasks.Get(taskId)
	if !ok {
		req.chToPutRes <- reqStatus{
			status: REQUEST_STATUS_UNKNOWN,
		}

		tr.doneRequests.Remove(req.reqId)
		return
	}

	res := resRaw.(CrackResult)

	req.chToPutRes <- reqStatus{
		status: REQUEST_STATUS_DONE,
		res:    res,
	}
}

func (tr *TasksRegistryActor) createNewWorker(ctx context.Context, hashRequested string,
	wordRange common.PartRange) {
	NewWorker(ctx, hashRequested, func(wd WorkDone) { tr.ae.SendReq(ctx, wd) }, wordRange)
}

func (tr *TasksRegistryActor) ProcessMessage(req any) {
	switch v := req.(type) {
	case taskReq:
		if !common.Done(v.ctx) {
			tr.processRequest(v)
		}
	case WorkDone:
		tr.processResult(v)
	case cancelTaskReq:
		if !common.Done(v.ctx) {
			tr.processCancelRequest(v)
		}
	case getReqStatus:
		if !common.Done(v.ctx) {
			tr.processGetStatusRequest(v)
		}
	default:
		panic("Unexpected event")
	}
}
