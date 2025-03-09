package workerapp

import (
	"CrackHash/internal/common"
	"context"
	"crypto/md5"
	"encoding/hex"
	"slices"

	"go.uber.org/zap"
)

const checkContextIterationCount = 37850000

const (
	RESULT_TYPE_TIMEOUT = iota
	RESULT_TYPE_CRACKED
	RESULT_TYPE_CANCELED
)

type CrackResult struct {
	ResultType int
	Words      []string
}

type WorkDone struct {
	Hash   string
	Range  common.PartRange
	Result CrackResult
}

type WorkerActor struct {
	logger   *zap.Logger
	ctx      context.Context
	hash     string
	onRes    func(WorkDone)
	reqRange common.PartRange
}

func NewWorker(ctx context.Context,
	hash string,
	onRes func(WorkDone),
	reqRange common.PartRange) {
	logger, err := zap.NewProduction()
	if err != nil {
		panic(err)
	}

	logger = logger.With(
		zap.String("actor", "worker"),
		zap.String("hash", hash),
		zap.Uint("partNumber", reqRange.PartNumber),
		zap.Uint("partCount", reqRange.PartCount),
	)

	actor := WorkerActor{
		logger,
		ctx,
		hash,
		onRes,
		reqRange,
	}

	actor.logger.Info("New worker created")
	go func() {
		defer actor.logger.Sync()
		actor.doWork()
	}()
}

func (w *WorkerActor) doWork() {

	w.logger.Info("Start cracking")

	hashBytes, err := hex.DecodeString(w.hash)
	if err != nil {
		panic(err)
	}

	wordsMatched := make([]string, 0)

	itersDone := 0
	for count := range w.reqRange.PartCount {

		if itersDone == checkContextIterationCount {

			select {
			case <-w.ctx.Done():
				switch w.ctx.Err() {
				case context.DeadlineExceeded:
					w.logger.Info("Deadline exceeded")
					w.onRes(w.createTimeoutRes(wordsMatched))
				case context.Canceled:
					w.logger.Info("Task canceled")
				}
				return
			default:
			}

			w.logger.Info("Iterations done", zap.Uint("count", count))

			itersDone = 0
		}

		wordToCheck := common.GetIthWord(w.reqRange.PartNumber + count)
		wordHash := md5.Sum([]byte(wordToCheck))
		if slices.Equal(hashBytes, wordHash[0:16]) {
			w.logger.Info("Found result", zap.String("result", wordToCheck))

			wordsMatched = append(wordsMatched, wordToCheck)
		}
		itersDone += 1
	}

	w.logger.Info("Cracking finished")
	w.onRes(w.createCrackedRes(wordsMatched))
}

func (w *WorkerActor) createCrackedRes(words []string) WorkDone {
	return WorkDone{
		Hash:  w.hash,
		Range: w.reqRange,
		Result: CrackResult{
			ResultType: RESULT_TYPE_CRACKED,
			Words:      words,
		},
	}
}

func (w *WorkerActor) createTimeoutRes(words []string) WorkDone {
	return WorkDone{
		Hash:  w.hash,
		Range: w.reqRange,
		Result: CrackResult{
			ResultType: RESULT_TYPE_TIMEOUT,
			Words:      words,
		},
	}
}
