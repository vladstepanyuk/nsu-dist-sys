package workerapi

import (
	"CrackHash/internal/common"
	"fmt"
	"net/http"
)

const (
	CrackHashPath = "internal/api/worker/hash/crack/task"

	CreateNewCrackHashTaskMethod = http.MethodPost
	CancelCrackHashRequestMethod = http.MethodDelete
	GetRequestStatusMethod       = http.MethodGet
)

type CrackHashRequest struct {
	Hash  string           `json:"hash"`
	Range common.PartRange `json:"range"`
}

type CrackHashResponseBody struct {
	RequestId string `json:"requestId"`
}

func CrackHash(client *http.Client, host string, req CrackHashRequest) (CrackHashResponseBody, error) {
	resp := CrackHashResponseBody{}
	err := common.DoReqWithRes(client, host, CreateNewCrackHashTaskMethod, CrackHashPath, req, &resp)
	return resp, err
}

func CancelRequest(client *http.Client, host string, reqId string) error {
	err := common.DoReqWithoutRes(client,
		host,
		CancelCrackHashRequestMethod,
		fmt.Sprintf("%s/%s", CrackHashPath, reqId),
		struct{}{})
	return err
}

const (
	REQUEST_STATUS_IN_PROGRESS = "InProgress"
	REQUEST_STATUS_DONE        = "Done"
	REQUEST_STATUS_UNKNOWN     = "Unknown"
)

type GetRequestStatusResponse struct {
	RequestStatus string   `json:"requestStatus"`
	ResultType    string   `json:"resultType"`
	Result        []string `json:"result"`
}

func GetReqStatus(client *http.Client, host string, reqId string) (GetRequestStatusResponse, error) {
	resp := GetRequestStatusResponse{}

	err := common.DoReqWithRes(client,
		host,
		GetRequestStatusMethod,
		fmt.Sprintf("%s/%s", CrackHashPath, reqId),
		struct{}{}, &resp)

	return resp, err
}

const (
	WorkerPath       = "internal/api/worker"
	PingWorkerMethod = http.MethodGet
)

func PingWorker(client *http.Client, host string) error {
	return common.DoReqWithoutRes(client, host, PingWorkerMethod, WorkerPath, struct{}{})
}
