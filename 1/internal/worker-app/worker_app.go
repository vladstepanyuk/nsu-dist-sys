package workerapp

import (
	"CrackHash/internal/common"
	"CrackHash/internal/manager-api"
	"CrackHash/internal/worker-api"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

func ResultTypeToMsg(t int) (string, error) {
	switch t {
	case RESULT_TYPE_TIMEOUT:
		return managerapi.RESULT_TYPE_TIMEOUT, nil
	case RESULT_TYPE_CRACKED:
		return managerapi.RESULT_TYPE_CRACKED, nil
	default:
		return "", fmt.Errorf("unsupported result type: %d", t)
	}
}

func RequestStatusToMsg(t int) (string, error) {
	switch t {
	case REQUEST_STATUS_DONE:
		return workerapi.REQUEST_STATUS_DONE, nil
	case REQUEST_STATUS_IN_PROGRESS:
		return workerapi.REQUEST_STATUS_IN_PROGRESS, nil
	case REQUEST_STATUS_UNKNOWN:
		return workerapi.REQUEST_STATUS_UNKNOWN, nil
	default:
		return "", fmt.Errorf("unsupported result type: %d", t)
	}
}

type WorkerApp struct {
	logger *zap.Logger

	cfg          *WorkerConfig
	taskRegistry *TasksRegistryActor
	NodeId       string

	pingReceived chan struct{}
}

func NewWorkerApp(cfg *WorkerConfig) (*WorkerApp, error) {
	logger, err := zap.NewProduction()
	if err != nil {
		return nil, err
	}

	nodeId := uuid.NewString()

	logger = logger.With(zap.String("actor", "workerApp"), zap.String("nodeId", nodeId))

	tr, err := NewTaskRegistryActor(cfg)
	if err != nil {
		return nil, err
	}
	return &WorkerApp{
		logger:       logger,
		cfg:          cfg,
		taskRegistry: tr,
		NodeId:       nodeId,
		pingReceived: make(chan struct{}),
	}, nil
}

func (wApp *WorkerApp) waitForCrackHashResult(resCh chan CrackResult, reqId string) {
	result := <-resCh

	if result.ResultType == RESULT_TYPE_CANCELED {
		return
	}

	resMsg, err := ResultTypeToMsg(result.ResultType)
	if err != nil {
		panic(err)
	}

	informResult := managerapi.InformCrackHashResultBody{
		RequestId:  reqId,
		ResultType: resMsg,
		Result:     result.Words,
	}

	wApp.logger.Info("Crack result received", zap.String("result type", resMsg), zap.Strings("data", result.Words))

	err = managerapi.InformCrackHashResult(http.DefaultClient, wApp.cfg.ManagerAddress, informResult)
	if err != nil {
		wApp.logger.Error("Error during informing manager", zap.Error(err))
	}
}

func (wApp *WorkerApp) createCrackHashTask(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)

	req := workerapi.CrackHashRequest{}
	err := decoder.Decode(&req)
	if err != nil {
		log.Println(err)

		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Bad body")
		return
	}

	if !common.IsMd5HashString(req.Hash) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Bad hash")
		return
	}

	resChan, reqIdCh := wApp.taskRegistry.CreateCrackHashTask(r.Context(), req.Hash, req.Range)
	var reqId string
	select {
	case reqId = <-reqIdCh:
	case <-r.Context().Done():
		w.WriteHeader(http.StatusRequestTimeout)
		return
	}
	w.WriteHeader(http.StatusOK)
	encoder := json.NewEncoder(w)
	encoder.Encode(workerapi.CrackHashResponseBody{RequestId: reqId})

	wApp.logger.Info("Remote addr", zap.String("addr", r.RemoteAddr))

	go wApp.waitForCrackHashResult(resChan, reqId)
}

func (wApp *WorkerApp) cancelTask(w http.ResponseWriter, r *http.Request) {
	reqId := chi.URLParam(r, "requestId")
	if reqId == "" {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Provide request id")
		return
	}

	panic("aboba")

	chanRes := wApp.taskRegistry.CancelTask(r.Context(), reqId)

	select {
	case <-chanRes:
		w.WriteHeader(http.StatusOK)
	case <-r.Context().Done():
		w.WriteHeader(http.StatusRequestTimeout)
	}
}

func (wApp *WorkerApp) getRequestStatus(w http.ResponseWriter, r *http.Request) {
	reqId := chi.URLParam(r, "requestId")
	if reqId == "" {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Provide request id")
		return
	}

	chanRes := wApp.taskRegistry.GetRequestStatus(r.Context(), reqId)

	select {
	case res := <-chanRes:

		msg, err := RequestStatusToMsg(res.status)
		if err != nil {
			panic(err)
		}

		respBody := workerapi.GetRequestStatusResponse{
			RequestStatus: msg,
		}
		if res.status == REQUEST_STATUS_DONE {
			msg, err = ResultTypeToMsg(res.res.ResultType)
			if err != nil {
				panic(err)
			}
			respBody.ResultType = msg
			respBody.Result = res.res.Words
		}
		w.WriteHeader(http.StatusOK)
		encoder := json.NewEncoder(w)
		encoder.Encode(respBody)

	case <-r.Context().Done():
		w.WriteHeader(http.StatusRequestTimeout)
	}
}

func createListener(port uint16) (l net.Listener, close func()) {
	l, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		panic(err)
	}
	return l, func() {
		_ = l.Close()
	}
}

func (wApp *WorkerApp) DoMain(port uint16) {
	defer wApp.logger.Sync()

	r := chi.NewRouter()

	// A good base middleware stack
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	// r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Use(middleware.Timeout(wApp.cfg.RequestTimeout))

	// RESTy routes for cracking hash tasks
	r.Route("/internal/api/worker", func(r chi.Router) {

		r.Get("/", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK) // process manager ping
			select {
			case wApp.pingReceived <- struct{}{}:
			case <-r.Context().Done():
			}

		})

		r.Route("/hash/crack", func(r chi.Router) {

			// Subrouters:
			r.Route("/task", func(r chi.Router) {
				r.Post("/", wApp.createCrackHashTask)
				r.Route("/{requestId}", func(r chi.Router) {
					r.Get("/", wApp.getRequestStatus)
					r.Delete("/", wApp.cancelTask)
				})
			})
		})
	})

	l, close := createListener(port)
	defer close()

	if port == 0 {
		port = uint16(l.Addr().(*net.TCPAddr).Port)
	}

	go wApp.workFunc(port)

	http.Serve(l, r)
}

func (wApp *WorkerApp) tryToRegister(port uint16) {
	for {
		regReq := managerapi.RegisterWorkerBody{
			NodeId: wApp.NodeId,
			Port:   port,
		}

		err := managerapi.RegisterWorker(http.DefaultClient, wApp.cfg.ManagerAddress, regReq)

		if err == nil {
			return
		}

		<-time.After(time.Millisecond * 500)
	}
}

func (wApp *WorkerApp) pingLoop() {
	for {
		select {
		case <-wApp.pingReceived:
		case <-time.After(time.Second * 10):
			return
		}
	}
}
func (wApp *WorkerApp) workFunc(port uint16) {

	for {

		wApp.tryToRegister(port)

		wApp.pingLoop()
	}

}
