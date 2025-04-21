package managerapp

import (
	"CrackHash/internal/common"
	managerapi "CrackHash/internal/manager-api"
	workerapi "CrackHash/internal/worker-api"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"
)

const recommendedTaskSize = 100_000

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

type TaskContext struct {
	cfg    *ManagerConfig
	logger *zap.Logger

	taskId TaskIdent

	pendingTasks map[common.PartRange]struct{}

	dataCollected []string

	status int
}

func DistributeTasks(ctx context.Context, cfg *ManagerConfig, ch *amqp.Channel, taskId TaskIdent) (TaskContext, error) {
	taskCtx := TaskContext{}
	logger, err := zap.NewProduction()
	if err != nil {
		return taskCtx, err
	}

	logger = logger.With(
		zap.String("actor", "workersProxy"),
		zap.String("hash", taskId.hash),
		zap.Uint("maxWorldLength", taskId.worldLength),
	)
	taskCtx.cfg = cfg
	taskCtx.logger = logger
	taskCtx.taskId = taskId
	taskCtx.pendingTasks = make(map[common.PartRange]struct{})
	taskCtx.dataCollected = make([]string, 0)
	taskCtx.status = REQUEST_STATUS_IN_PROGRESS

	return taskCtx, taskCtx.distributeTasks(ch)

}

func (tCtx *TaskContext) distributeTasks(ch *amqp.Channel) error {
	var startRangeToSplit uint = 0
	var countToSplit uint = 0

	ranges := []common.PartRange{}
	for i := range tCtx.taskId.worldLength {
		countToSplit += uint(math.Pow(36, float64(i+1)))

		for j := uint(0); j < uint(countToSplit); j += recommendedTaskSize {
			nextJ := min(j+recommendedTaskSize, countToSplit)
			r := common.PartRange{
				PartNumber: startRangeToSplit + j,
				PartCount:  nextJ - j,
			}

			ranges = append(ranges, r)
		}

		startRangeToSplit += countToSplit
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond*100)
	defer cancel()

	errChan := make(chan error, len(ranges))
	var wg sync.WaitGroup

	for _, r := range ranges {
		tCtx.pendingTasks[r] = struct{}{}
		wg.Add(1)
		go func(r common.PartRange) {
			err := tCtx.publishTask(ctx, r, ch)
			defer wg.Done()
			errChan <- err
		}(r)
	}

	wg.Wait()

	for _ = range len(ranges) {
		err := <-errChan
		if err != nil {
			return err
		}
	}

	return nil
}

func (tCtx *TaskContext) publishTask(ctx context.Context, r common.PartRange, ch *amqp.Channel) error {
	req := workerapi.CrackHashRequest{
		Hash:  tCtx.taskId.hash,
		Range: r,
	}
	var buffer bytes.Buffer
	json.NewEncoder(&buffer).Encode(req)

	return ch.PublishWithContext(ctx,
		tCtx.cfg.RabbitMqTaskExchange,
		"",
		false,
		false,
		amqp.Publishing{
			DeliveryMode: amqp.Persistent,
			Body: buffer.Bytes(),
		})

}

func (tCtx *TaskContext) GetStatus() (requestStatus int, dataCollected []string) {
	return tCtx.status, tCtx.dataCollected
}

func (tCtx *TaskContext) InformResult(res managerapi.InformCrackHashResultBody) bool {

	if _, ok := tCtx.pendingTasks[res.Range]; tCtx.taskId.hash != res.Hash || !ok {
		return false
	}
	if tCtx.status != REQUEST_STATUS_IN_PROGRESS {
		return true
	}

	delete(tCtx.pendingTasks, res.Range)
	if res.ResultType == managerapi.RESULT_TYPE_TIMEOUT {
		tCtx.status = REQUEST_STATUS_TOO_HARD
		return true
	}

	tCtx.dataCollected = append(tCtx.dataCollected, res.Result...)
	if len(tCtx.pendingTasks) == 0 {
		tCtx.status = REQUEST_STATUS_READY
		return true
	}
	return false
}
