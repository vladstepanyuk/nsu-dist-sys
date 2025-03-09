package common

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

func DoReqWithoutRes(client *http.Client, host string, method string, path string, body any) error {
	var buffer bytes.Buffer

	err := json.NewEncoder(&buffer).Encode(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(
		method,
		fmt.Sprintf("http://%s/%s",
			host,
			path,
		), &buffer)

	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func DoReqWithRes(client *http.Client, host string, method string, path string, body any, res any) error {
	var buffer bytes.Buffer

	err := json.NewEncoder(&buffer).Encode(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(
		method,
		fmt.Sprintf("http://%s/%s",
			host,
			path,
		), &buffer)

	if err != nil {
		return err
	}


	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	decoder := json.NewDecoder(resp.Body)
	return decoder.Decode(res)
}
