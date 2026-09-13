package researchgateway

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
)

const (
	cursorVersion  = 1
	maxCursorBytes = 4096
)

type cursorPayload struct {
	Version     int    `json:"v"`
	Operation   string `json:"op"`
	SnapshotID  string `json:"snapshot"`
	Fingerprint string `json:"request"`
	Offset      int64  `json:"offset"`
	Checksum    string `json:"checksum"`
}

func makeCursor(operation, snapshotID, fingerprint string, offset int64) (string, error) {
	payload := cursorPayload{
		Version:     cursorVersion,
		Operation:   operation,
		SnapshotID:  snapshotID,
		Fingerprint: fingerprint,
		Offset:      offset,
	}
	payload.Checksum = cursorChecksum(payload)
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func parseCursor(raw, operation, snapshotID, fingerprint string) (int64, error) {
	if raw == "" || len(raw) > maxCursorBytes {
		return 0, ErrInvalidCursor
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(data) > maxCursorBytes {
		return 0, ErrInvalidCursor
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var payload cursorPayload
	if err := decoder.Decode(&payload); err != nil {
		return 0, ErrInvalidCursor
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return 0, ErrInvalidCursor
	}
	wantChecksum := cursorChecksum(payload)
	if subtle.ConstantTimeCompare([]byte(payload.Checksum), []byte(wantChecksum)) != 1 ||
		payload.Version != cursorVersion || payload.Operation != operation ||
		payload.SnapshotID != snapshotID || payload.Fingerprint != fingerprint || payload.Offset < 0 {
		return 0, ErrInvalidCursor
	}
	return payload.Offset, nil
}

func cursorChecksum(payload cursorPayload) string {
	digest := sha256.New()
	_, _ = digest.Write([]byte("codexkit/researchgateway/cursor/v1\x00"))
	_, _ = fmt.Fprintf(digest, "%d\x00%s\x00%s\x00%s\x00%d", payload.Version, payload.Operation, payload.SnapshotID, payload.Fingerprint, payload.Offset)
	return hex.EncodeToString(digest.Sum(nil))
}

func requestFingerprint(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}
