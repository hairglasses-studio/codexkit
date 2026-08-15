package agyloop

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"time"
)

var defaultSecretKey = []byte("hairglasses-studio-attestation-hmac-v1")

func getSigningKey() []byte {
	if k := os.Getenv("RALPH_RECEIPT_HMAC_KEY"); k != "" {
		return []byte(k)
	}
	if k := os.Getenv("AGY_ATTESTATION_KEY"); k != "" {
		return []byte(k)
	}
	return defaultSecretKey
}

func SignReceipt(repo, branch, gitSHA, prompt string, iteration int) string {
	ts := time.Now().UTC().Format(time.RFC3339)
	payload := fmt.Sprintf("%s:%s:%s:%s:%d:%s", repo, branch, gitSHA, prompt, iteration, ts)
	mac := hmac.New(sha256.New, getSigningKey())
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

func VerifyReceipt(repo, branch, gitSHA, prompt string, iteration int, timestamp, signature string) bool {
	payload := fmt.Sprintf("%s:%s:%s:%s:%d:%s", repo, branch, gitSHA, prompt, iteration, timestamp)
	mac := hmac.New(sha256.New, getSigningKey())
	mac.Write([]byte(payload))
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}
