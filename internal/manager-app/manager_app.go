package managerapp

import (
	"CrackHash/internal/common"
	managerapi "CrackHash/internal/manager-api"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/netip"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"
)

type ManagerApp struct {
	logger *zap.Logger
	cfg    *ManagerConfig
	wr     *WorkersRegistry

	htm *HashTasksManager
}

func NewManagerApp(cfg *ManagerConfig) (*ManagerApp, error) {

	logger, err := zap.NewProduction()
	if err != nil {
		return nil, err
	}
	logger = logger.With(zap.String("actor", "managerApp"))

	wr := NewWorkersRegistry()
	htm, err := NewHashTasksManager(cfg, wr)
	if err != nil {
		return nil, err
	}
	return &ManagerApp{
		logger,
		cfg,
		wr,
		htm,
	}, nil
}

func (mApp *ManagerApp) processRegisterWorkerRequest(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	req := managerapi.RegisterWorkerBody{}

	err := decoder.Decode(&req)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Bad request")

		mApp.logger.Info("Can't parse request body", zap.Error(err))
		return
	}

	addr, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		panic(err)
	}

	ok := mApp.wr.RegisterWorker(req.NodeId, netip.AddrPortFrom(addr.Addr(), uint16(req.Port)))
	if !ok {
		w.WriteHeader(http.StatusConflict)
		fmt.Fprintf(w, "Worker with nodeId %s already registered", req.NodeId)

		return
	}

	w.WriteHeader(http.StatusOK)
}

func (mApp *ManagerApp) processUnregisterWorkerRequest(w http.ResponseWriter, r *http.Request) {
	defer func() {
		r.Body.Close()
	}()
	nodeId := chi.URLParam(r, "nodeId")
	if nodeId == "" {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Provide node id")
		mApp.logger.Warn("no node id")
		return
	}

	ok := mApp.wr.UnregisterWorker(nodeId)
	if !ok {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Unknown worker")
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (mApp *ManagerApp) crackHash(w http.ResponseWriter, r *http.Request) {
	defer func() {
		r.Body.Close()
	}()
	body := managerapi.CrackHashRequestBody{}
	decoder := json.NewDecoder(r.Body)
	err := decoder.Decode(&body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if !common.IsMd5HashString(body.Hash) || body.MaxLength == 0 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	ch := mApp.htm.CrackHash(r.Context(), TaskIdent{body.Hash, body.MaxLength})
	var reqId string
	select {
	case <-r.Context().Done():
		w.WriteHeader(http.StatusRequestTimeout)
		return
	case resp := <-ch:
		reqId = resp.reqId
	}

	encoder := json.NewEncoder(w)
	err = encoder.Encode(managerapi.CrackHashResponseBody{RequestId: reqId})
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
}

func convertReqStatusToStr(reqStatus int) string {
	switch reqStatus {
	case REQUEST_STATUS_IN_PROGRESS:
		return managerapi.REQUEST_STATUS_IN_PROGRESS
	case REQUEST_STATUS_READY:
		return managerapi.REQUEST_STATUS_READY
	case REQUEST_STATUS_TOO_HARD:
		return managerapi.REQUEST_STATUS_TOO_HARD
	default:
		panic("unknown status")
	}
}

func (mApp *ManagerApp) getRequestStatus(w http.ResponseWriter, r *http.Request) {
	defer func() {
		r.Body.Close()
	}()
	requestId := chi.URLParam(r, "requestId")

	ch := mApp.htm.GetRequestStatus(r.Context(), requestId)
	var resp GetRequestStatusResponse
	select {
	case <-r.Context().Done():
		w.WriteHeader(http.StatusRequestTimeout)
		return
	case resp = <-ch:
	}

	if resp.Status == REQUEST_STATUS_UNKNOWN {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	encoder := json.NewEncoder(w)
	err := encoder.Encode(managerapi.GetRequestStatusResponseBody{
		Status: convertReqStatusToStr(resp.Status),
		Data:   resp.Data})
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
}

func (mApp *ManagerApp) informResult(w http.ResponseWriter, r *http.Request) {
	defer func() {
		r.Body.Close()
	}()

	body := managerapi.InformCrackHashResultBody{}
	decoder := json.NewDecoder(r.Body)
	err := decoder.Decode(&body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	mApp.htm.InformResult(body)
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

func (mApp *ManagerApp) DoMain(port uint16) {
	defer mApp.logger.Sync()

	r := chi.NewRouter()

	// A good base middleware stack
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Use(middleware.Timeout(mApp.cfg.RequestTimeout))

	r.Route("/internal/api/manager/hash/crack", func(r chi.Router) {

		r.Route("/request", func(r chi.Router) {
			r.Patch("/", mApp.informResult)
		})

		r.Route("/worker", func(r chi.Router) {
			r.Post("/", mApp.processRegisterWorkerRequest)
			r.Route("/{nodeId}", func(r chi.Router) {
				r.Delete("/", mApp.processUnregisterWorkerRequest)
			})
		})
	})

	r.Route("/api/hash/crack", func(r chi.Router) {
		r.Post("/", mApp.crackHash)
		r.Route("/{requestId}", func(r chi.Router) {
			r.Get("/", mApp.getRequestStatus)
		})
	})

	l, close := createListener(port)
	defer close()

	http.Serve(l, r)
}
