package managerapp

import (
	"CrackHash/internal/common"
	managerapi "CrackHash/internal/manager-api"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	amqp "github.com/rabbitmq/amqp091-go"
)

type ManagerApp struct {
	logger *zap.Logger
	cfg    *ManagerConfig

	conn *amqp.Connection
	ch   *amqp.Channel

	htm *HashTasksManager
}

func NewManagerApp(cfg *ManagerConfig) (*ManagerApp, error) {

	logger, err := zap.NewProduction()
	if err != nil {
		return nil, err
	}
	logger = logger.With(zap.String("actor", "managerApp"))

	return &ManagerApp{
		logger,
		cfg,
		nil,
		nil,
		nil,
	}, nil
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

func createListener(port uint16) (l net.Listener, close func()) {
	l, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		panic(err)
	}
	return l, func() {
		_ = l.Close()
	}
}

func (mApp *ManagerApp) DoMain(port uint16, conn *amqp.Connection) {
	defer mApp.logger.Sync()

	r := chi.NewRouter()

	// A good base middleware stack
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Use(middleware.Timeout(mApp.cfg.RequestTimeout))

	r.Route("/api/hash/crack", func(r chi.Router) {
		r.Post("/", mApp.crackHash)
		r.Route("/{requestId}", func(r chi.Router) {
			r.Get("/", mApp.getRequestStatus)
		})
	})

	l, close := createListener(port)
	defer close()

	mApp.conn = conn

	ch, err := conn.Channel()
	if err != nil {
		log.Fatal(err)
	}

	err = ch.Confirm(false)
	if err != nil {
		log.Fatal(err)
	}

	htm, err := NewHashTasksManager(mApp.cfg, ch)
	if err != nil {
		panic(err)
	}

	mApp.htm = htm

	http.Serve(l, r)
}
