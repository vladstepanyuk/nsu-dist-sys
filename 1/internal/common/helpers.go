package common

import (
	"context"
	"encoding/hex"
	"math/big"
)

type PartRange struct {
	PartNumber uint `json:"partNumber"`
	PartCount  uint `json:"partCount"`
}

func DeleteFromSlice[V any](s []V, i int) []V {
	s[i] = s[len(s)-1]
	return s[0 : len(s)-1]
}

func Done(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		println("done")
		return true
	default:
		return false
	}
}

func IsMd5HashString(hash string) bool {

	bytes, err := hex.DecodeString(hash)
	if err != nil || len(bytes) > 16 { // ? != 16
		return false
	}

	return true
}

func GetIthWord(i uint) string {
	return big.NewInt(int64(i)).Text(36)
}

type ctxKeyRequestID int

const requestIDKey ctxKeyRequestID = 0

func GetReqID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if reqID, ok := ctx.Value(requestIDKey).(string); ok {
		return reqID
	}
	return ""
}

func WithReqId(ctx context.Context, reqId string) context.Context {
	return context.WithValue(ctx, requestIDKey, reqId)
}
