package managerapp

import (
	"CrackHash/internal/common"
	managerapi "CrackHash/internal/manager-api"
	"bytes"
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	lru "github.com/hashicorp/golang-lru"
	amqp "github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"
)

type informResult struct {
	res managerapi.InformCrackHashResultBody
	d   amqp.Delivery
}
type taskTimeOuted struct {
	taskId TaskIdent
}

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
	tCtx     TaskContext
	reqs     []string
	taskDone context.CancelFunc
}

type HashTasksManager struct {
	cfg    *ManagerConfig
	logger *zap.Logger

	pendingTasks    map[TaskIdent]*taskContext
	pendingRequests map[string]TaskIdent

	doneRequests *lru.ARCCache
	doneTasks    *lru.ARCCache

	ch          *amqp.Channel
	resultQueue amqp.Queue

	ae *common.ActorExecutor
}

func NewHashTasksManager(cfg *ManagerConfig, ch *amqp.Channel) (*HashTasksManager, error) {
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

		ch: ch,

		ae: nil,
	}

	err = htm.setupQueue()
	if err != nil {
		return nil, err
	}

	msgs, err := htm.ch.Consume(
		htm.resultQueue.Name,
		"",    // consumer
		false, // auto-ack
		false, // exclusive
		false, // no-local
		false, // no-wait
		nil,   // args
	)
	if err != nil {
		return nil, err
	}

	go htm.consumeResult(msgs)

	htm.ae = common.NewActorExecutor(htm)
	htm.ae.Start()
	return htm, err
}

func (htm *HashTasksManager) setupQueue() error {
	q, err := htm.ch.QueueDeclare(
		htm.cfg.RabbitMqResultQueue,
		true, false, false, false, nil,
	)
	if err != nil {
		return err
	}

	err = htm.ch.ExchangeDeclare(
		htm.cfg.RabbitMqResultExchange,
		"direct",
		true,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		return err
	}

	err = htm.ch.ExchangeDeclare(
		htm.cfg.RabbitMqTaskExchange,
		"direct",
		true,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		return err
	}

	err = htm.ch.QueueBind(
		q.Name,
		"",
		htm.cfg.RabbitMqResultExchange,
		false,
		nil,
	)
	if err != nil {
		return err
	}

	htm.resultQueue = q

	return nil
}

func (htm *HashTasksManager) consumeResult(msgs <-chan amqp.Delivery) {

	for d := range msgs {
		informRes := managerapi.InformCrackHashResultBody{}
		json.NewDecoder(bytes.NewBuffer(d.Body)).Decode(&informRes)

		htm.ae.SendReq(context.Background(), informResult{
			informRes,
			d,
		})
	}
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

	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond*100)
	defer cancel()

	ctxForTimeout, taskDone := context.WithCancel(context.Background())

	go func() {
		select {
		case <-time.After(htm.cfg.CrackHashTimeout):
			htm.ae.SendReq(ctxForTimeout, taskTimeOuted{taskId: req.taskId})
		case <-ctxForTimeout.Done():
		}

	}()

	taskCtx, err := DistributeTasks(ctx, htm.cfg, htm.ch, req.taskId)
	if err != nil {
		taskDone()
		return
	}

	htm.pendingRequests[reqId] = req.taskId

	task, ok := htm.pendingTasks[req.taskId]
	if ok {
		task.reqs = append(task.reqs, reqId)
		taskDone()
		return
	}

	htm.pendingTasks[req.taskId] = &taskContext{
		taskCtx,
		[]string{reqId},
		taskDone,
	}
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

func (htm *HashTasksManager) taskTimeOuted(req taskTimeOuted) {
	tCtx, ok := htm.pendingTasks[req.taskId]
	if !ok {
		return
	}
	tCtx.taskDone()

	delete(htm.pendingTasks, req.taskId)
}

func (htm *HashTasksManager) processInformResult(res managerapi.InformCrackHashResultBody) {
	htm.logger.Info(
		"got result from worker",
		zap.String("hash", res.Hash),
		zap.Any("range", res.Range),
		zap.String("resultType", res.ResultType),
	)

	tasksCompleted := make([]TaskIdent, 0)

	for tIdent, tCtx := range htm.pendingTasks {
		reqCompleted := tCtx.tCtx.InformResult(res)
		if !reqCompleted {
			continue
		}

		tasksCompleted = append(tasksCompleted, tIdent)
		for _, reqId := range tCtx.reqs {
			delete(htm.pendingRequests, reqId)
			htm.doneRequests.Add(reqId, tIdent)
		}

		taskRes := GetRequestStatusResponse{}
		taskRes.Status, taskRes.Data = tCtx.tCtx.GetStatus()

		htm.doneTasks.Add(tIdent, taskRes)
	}

	for _, tIdent := range tasksCompleted {
		delete(htm.pendingTasks, tIdent)
	}
}

func (htm *HashTasksManager) ProcessMessage(req any) {
	switch v := req.(type) {
	case crackHashRequest:
		if !common.Done(v.ctx) {
			htm.processCrackHashReq(&v)
		}
	case getRequestStatusRequest:
		if !common.Done(v.ctx) {
			htm.processGetStatusRequest(v)
		}
	case informResult:
		htm.processInformResult(v.res)
		v.d.Ack(false)

	case taskTimeOuted:
		htm.taskTimeOuted(v)

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
