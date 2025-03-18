package managerapp

import (
	"CrackHash/internal/common"
	managerapi "CrackHash/internal/manager-api"
	workerapi "CrackHash/internal/worker-api"
	"context"
	"fmt"
	"math"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"
)

var ErrNoWorkersError = fmt.Errorf("no available workers to crack hash")

var ErrCantCrack = fmt.Errorf("task is too complex, can't crack hash")

var ErrCanceled = fmt.Errorf("canceled")

type TaskIdent struct {
	hash        string
	worldLength uint
}

type CrackHashResult struct {
	TaskId TaskIdent
	Err    error
	Data   []string
}

type WorkersProxy struct {
	cfg    *ManagerConfig
	logger *zap.Logger
	wr     *WorkersRegistry
	taskId TaskIdent
	ctx    context.Context

	tasksResultNeedToWait map[common.PartRange]struct{}
	completeTasks         map[common.PartRange]struct{}
	pendingReqIds         map[string]common.PartRange
	pendingTasks          map[common.PartRange]struct {
		WorkersInfo
		string
	}

	dataCollected []string

	wq workersQueue

	distributionCount uint

	failureCount uint

	chToPutRes   chan CrackHashResult
	informResult chan managerapi.InformCrackHashResultBody
}

func NewWorkersProxy(ctx context.Context, cfg *ManagerConfig, wr *WorkersRegistry, taskId TaskIdent) (
	chToPutRes chan CrackHashResult,
	informResult chan managerapi.InformCrackHashResultBody,
	err error) {
	logger, err := zap.NewProduction()
	if err != nil {
		return nil, nil, err
	}

	logger = logger.With(
		zap.String("actor", "workersProxy"),
		zap.String("hash", taskId.hash),
		zap.Uint("maxWorldLength", taskId.worldLength),
	)

	chToPutRes = make(chan CrackHashResult)
	informResult = make(chan managerapi.InformCrackHashResultBody)

	wp := WorkersProxy{
		cfg:           cfg,
		logger:        logger,
		wr:            wr,
		taskId:        taskId,
		ctx:           ctx,
		completeTasks: make(map[common.PartRange]struct{}),
		pendingTasks: make(map[common.PartRange]struct {
			WorkersInfo
			string
		}),
		pendingReqIds: make(map[string]common.PartRange),
		dataCollected: make([]string, 0),
		failureCount:  0,
		chToPutRes:    chToPutRes,
		informResult:  informResult,
	}

	go wp.workFunc()

	return chToPutRes, informResult, nil
}

func calculateEndWordIndex(maxWorldLength uint) uint {
	var res uint = 0
	for i := range maxWorldLength {
		res += uint(math.Pow(36, float64(i+1)))
	}
	return res
}

func (wp *WorkersProxy) tryDistributeRanges(ranges []common.PartRange) error {
	results := make(chan struct {
		common.PartRange
		WorkersInfo
		string
		error
	}, len(ranges))

	wg := sync.WaitGroup{}
	wg.Add(len(ranges))

	for _, r := range ranges {
		nextWorker, ok := wp.wq.getWorker()
		if !ok {
			return ErrNoWorkersError
		}

		go func(worker WorkersInfo, partRange common.PartRange) {
			wp.logger.Info("requesting to crack hash", zap.Any("partRange", partRange), zap.String("worker", worker.nodeId))
			resp, err := workerapi.CrackHash(
				http.DefaultClient,
				worker.addr.String(),
				workerapi.CrackHashRequest{Hash: wp.taskId.hash, Range: partRange},
			)

			results <- struct {
				common.PartRange
				WorkersInfo
				string
				error
			}{partRange, worker, resp.RequestId, err}
			wg.Done()
		}(nextWorker, r)
	}

	wg.Wait()

	var err error = nil

	for range len(ranges) {
		res := <-results
		if res.error != nil {
			wp.logger.Warn(
				"Got error while trying to distribute task",
				zap.String("workerId", res.nodeId),
				zap.Error(res.error),
			)
			err = res.error
			continue
		}
		wp.pendingTasks[res.PartRange] = struct {
			WorkersInfo
			string
		}{res.WorkersInfo, res.string}
		wp.pendingReqIds[res.string] = res.PartRange
	}

	return err
}

func (wp *WorkersProxy) fillRangesNeedToWait() {
	endWordIndex := calculateEndWordIndex(wp.taskId.worldLength)

	wp.tasksResultNeedToWait = make(map[common.PartRange]struct{})

	for i := uint(0); i < endWordIndex; i += wp.distributionCount {
		nextI := min(i+wp.distributionCount, endWordIndex)
		r := common.PartRange{
			PartNumber: i,
			PartCount:  nextI - i,
		}

		wp.tasksResultNeedToWait[r] = struct{}{}
	}
}

func (wp *WorkersProxy) calculateRangesToDistribute() []common.PartRange {
	endWordIndex := calculateEndWordIndex(wp.taskId.worldLength)

	res := make([]common.PartRange, 0, endWordIndex/wp.distributionCount)

	for task := range wp.tasksResultNeedToWait {
		if _, ok := wp.pendingTasks[task]; ok {
			continue
		}
		res = append(res, task)
	}

	return res
}

func (wp *WorkersProxy) updateWorkersQueueIfNeeded() error {
	if wp.failureCount%100 == 0 {
		wp.logger.Warn("updating workers queue because of too much failures count")
		<-time.After(time.Second)
		workers := wp.wr.ListWorkers()
		if len(workers) == 0 {
			return ErrNoWorkersError
		}

		wp.wq.resetWorkers(workers)
	}

	return nil
}

func (wp *WorkersProxy) distributeRangesIfNeeded() error {
	for {
		if wp.failureCount >= wp.cfg.MaxFailureCount {
			wp.logger.Error("too much failures. stopping now")
			return ErrNoWorkersError
		}

		rd := wp.calculateRangesToDistribute()

		wp.logger.Info(
			"trying to distribute tasks among workers",
			zap.Int("tasksCount", len(rd)),
		)

		err := wp.tryDistributeRanges(rd)
		if err != nil {
			wp.logger.Warn("got a failure, trying again")
			wp.failureCount += 1
			if err := wp.updateWorkersQueueIfNeeded(); err != nil {
				return err
			}
			continue
		}

		return nil
	}
}

func (wp *WorkersProxy) cleanupPendingTasks() {

	wp.logger.Warn("cleanup pending tasks out of failure workers")

	wg := sync.WaitGroup{}

	wg.Add(len(wp.pendingTasks))
	reqsToDelete := make(chan string, len(wp.pendingTasks))

	wp.logger.Info(
		"checking status pending req status",
		zap.Int("tasksCount", len(wp.pendingTasks)),
	)
	for r, taskInfo := range wp.pendingTasks {

		go func(partRange common.PartRange, taskInfo struct {
			WorkersInfo
			string
		}) {
			defer wg.Done()
			status, err := workerapi.GetReqStatus(
				http.DefaultClient,
				taskInfo.WorkersInfo.addr.String(),
				taskInfo.string,
			)

			if err != nil ||
				status.RequestStatus == workerapi.REQUEST_STATUS_UNKNOWN {
				wp.logger.Info(
					"failure worker",
					zap.String("nodeId", taskInfo.nodeId),
					zap.Error(err),
					zap.String("status", status.RequestStatus))
				reqsToDelete <- taskInfo.string
				return
			}

			if status.RequestStatus == workerapi.REQUEST_STATUS_DONE {
				go wp.InformResult(managerapi.InformCrackHashResultBody{
					RequestId:  taskInfo.string,
					ResultType: status.ResultType,
					Result:     status.Result,
				})
			}

		}(r, taskInfo)
	}
	wg.Wait()

	for {
		select {
		case reqId := <-reqsToDelete:
			r := wp.pendingReqIds[reqId]
			delete(wp.pendingReqIds, reqId)
			delete(wp.pendingTasks, r)
		default:
			return
		}
	}
}

func (wp *WorkersProxy) processInformResult(res managerapi.InformCrackHashResultBody) (err error, needToStop bool) {

	wp.logger.Info(
		"got new result from worker",
		zap.String("reqId", res.RequestId),
		zap.String("resultType", res.ResultType),
	)

	r, ok := wp.pendingReqIds[res.RequestId]

	if !ok {
		return nil, false
	}

	if res.ResultType != managerapi.RESULT_TYPE_CRACKED {
		return ErrCantCrack, true
	}

	wp.completeTasks[r] = struct{}{}
	delete(wp.pendingTasks, r)
	delete(wp.pendingReqIds, res.RequestId)

	wp.dataCollected = append(wp.dataCollected, res.Result...)

	if wp.tasksResultNeedToWait != nil {
		delete(wp.tasksResultNeedToWait, r)
		return nil, len(wp.tasksResultNeedToWait) == 0
	}

	wp.calculateRangesToDistribute()
	return nil, len(wp.tasksResultNeedToWait) == 0
}

func (wp *WorkersProxy) workFunc() {

	replyErr := func(err error) {
		wp.chToPutRes <- CrackHashResult{TaskId: wp.taskId, Err: err}
	}

	workers := wp.wr.ListWorkers()
	if len(workers) == 0 {
		replyErr(ErrNoWorkersError)
		return
	}

	wp.wq = newWorkerQueue(workers)

	endWorldIndex := calculateEndWordIndex(wp.taskId.worldLength)
	wp.distributionCount = max(endWorldIndex/uint(len(workers)*2), 100)

	wp.fillRangesNeedToWait()

	err := wp.distributeRangesIfNeeded()

	if err != nil {
		replyErr(err)
		return
	}

	for {

		if wp.failureCount >= wp.cfg.MaxFailureCount {
			replyErr(ErrNoWorkersError)
			return
		}

		select {
		case res := <-wp.informResult:
			err, needToStop := wp.processInformResult(res)
			if err != nil {
				replyErr(err)
				return
			}

			if needToStop {

				wp.chToPutRes <- CrackHashResult{TaskId: wp.taskId,
					Err:  nil,
					Data: wp.dataCollected,
				}
				return
			}
		case <-time.After(time.Second * 10):
			wp.cleanupPendingTasks()

			wp.failureCount += 1
			err := wp.distributeRangesIfNeeded()

			if err != nil {
				replyErr(err)
				return
			}
		case <-wp.ctx.Done():
			wp.logger.Info("task was canceled")
			replyErr(ErrCanceled)
			return
		}

	}

}

func (wp *WorkersProxy) InformResult(res managerapi.InformCrackHashResultBody) {
	wp.informResult <- res
}

type workersQueue struct {
	idx     int
	workers []WorkersInfo
}

func newWorkerQueue(workers []WorkersInfo) workersQueue {
	return workersQueue{idx: 0, workers: workers}
}

func (wq *workersQueue) getWorker() (WorkersInfo, bool) {
	if len(wq.workers) == 0 {
		return WorkersInfo{}, false
	}

	worker := wq.workers[wq.idx]
	wq.idx = (wq.idx + 1) % len(wq.workers)

	return worker, true
}

func (wq *workersQueue) resetWorkers(workers []WorkersInfo) {
	*wq = newWorkerQueue(workers)
}
