package managerapi

import (
	"CrackHash/internal/common"
	"fmt"
	"net/http"
)

const (
	CrackHashPath          = "api/hash/crack"
	CrackHashMethod        = http.MethodPost
	GetRequestStatusMethod = http.MethodGet
)

type CrackHashRequestBody struct {
	Hash      string `json:"hash"`
	MaxLength uint   `json:"maxLength"`
}

type CrackHashResponseBody struct {
	RequestId string `json:"requestId"`
}

const (
	REQUEST_STATUS_READY       = "READY"
	REQUEST_STATUS_IN_PROGRESS = "IN_PROGRESS"
	REQUEST_STATUS_TOO_HARD    = "TOO_HARD"
)

type GetRequestStatusResponseBody struct {
	Status string   `json:"status"`
	Data   []string `json:"data"`
}

const (
	WorkerPath           = "internal/api/manager/hash/crack/worker"
	RegisterWorkerMethod = http.MethodPost
	ForgetWorkerMethod   = http.MethodDelete
)

type RegisterWorkerBody struct {
	NodeId string `json:"nodeId"`
	Port   uint16 `json:"port"`
}

func RegisterWorker(client *http.Client, host string, body RegisterWorkerBody) error {
	return common.DoReqWithoutRes(client, host, RegisterWorkerMethod, WorkerPath, body)
}

func ForgetWorker(client *http.Client, host string, nodeId string) error {
	return common.DoReqWithoutRes(
		client,
		host,
		ForgetWorkerMethod,
		fmt.Sprintf("%s/%s", WorkerPath, nodeId),
		struct{}{})
}

const (
	InformCrackHashResultPath   = "internal/api/manager/hash/crack/request"
	InformCrackHashResultMethod = http.MethodPatch
)

const (
	RESULT_TYPE_TIMEOUT = "Timeout"
	RESULT_TYPE_CRACKED = "Cracked"
)

type InformCrackHashResultBody struct {
	RequestId  string   `json:"requestId"`
	ResultType string   `json:"result-type"`
	Result     []string `json:"result"`
}

func InformCrackHashResult(client *http.Client, host string, body InformCrackHashResultBody) error {
	return common.DoReqWithoutRes(client, host, InformCrackHashResultMethod, InformCrackHashResultPath, body)
}
