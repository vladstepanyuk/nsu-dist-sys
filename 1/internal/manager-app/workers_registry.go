package managerapp

import (
	workerapi "CrackHash/internal/worker-api"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"go.uber.org/zap"
)

type WorkersInfo struct {
	nodeId string
	addr   netip.AddrPort
}

type WorkersRegistry struct {
	logger  *zap.Logger
	workers sync.Map
}

func NewWorkersRegistry() *WorkersRegistry {

	logger, err := zap.NewProduction()
	if err != nil {
		panic(err)
	}

	logger = logger.With(zap.String("actor", "WorkersRegistry"))
	wr := &WorkersRegistry{logger: logger, workers: sync.Map{}}

	go wr.healthCheckRoutine()

	return wr
}

func (wr *WorkersRegistry) healthCheckRoutine() {
	for {
		workers := wr.ListWorkers()

		wg := sync.WaitGroup{}
		wg.Add(len(workers))
		deleteWorkers := make(chan string, len(workers))
		for i := range workers {
			go func(w *WorkersInfo) {
				err := workerapi.PingWorker(http.DefaultClient, w.addr.String())

				if err != nil {
					wr.logger.Warn(
						"failure detected",
						zap.String("nodeId", w.nodeId),
						zap.String("addr", w.addr.String()),
						zap.Error(err),
					)
					deleteWorkers <- w.nodeId
				}

				wg.Done()
			}(&workers[i])
		}

		wg.Wait()
	deleteLoop:
		for {
			select {
			case nodeId := <-deleteWorkers:
				wr.UnregisterWorker(nodeId)
			default:
				break deleteLoop
			}
		}

		<-time.After(time.Second)
	}
}

func (wr *WorkersRegistry) RegisterWorker(nodeId string, addr netip.AddrPort) (ok bool) {
	wr.logger.Info("register new worker", zap.String("nodeId", nodeId), zap.String("addr", addr.String()))
	actual, loaded := wr.workers.LoadOrStore(nodeId, addr)

	if loaded {
		addrActual := actual.(netip.AddrPort)
		return addr == addrActual
	}

	return true
}

func (wr *WorkersRegistry) UnregisterWorker(nodeId string) (ok bool) {
	_, loaded := wr.workers.LoadAndDelete(nodeId)

	return !loaded
}

func (wr *WorkersRegistry) ListWorkers() []WorkersInfo {

	res := make([]WorkersInfo, 0)
	wr.workers.Range(func(key, value any) bool {
		nodeId := key.(string)
		addr := value.(netip.AddrPort)
		res = append(res, WorkersInfo{nodeId, addr})
		return true
	})

	return res
}
