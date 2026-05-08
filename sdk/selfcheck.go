package sdk

import (
	"encoding/json"
	"time"
)

type SelfCheckReport struct {
	PrivateKeyLoaded bool   `json:"private_key_loaded"`
	PrivateKeyValid  bool   `json:"private_key_valid"`
	CanDecodeSecret  bool   `json:"can_decode_secret"`
	LastError        string `json:"last_error,omitempty"`
	TimestampUnix    int64  `json:"timestamp_unix"`
}

func PreflightSelfCheck(sampleSecretToken string) string {
	r := SelfCheckReport{TimestampUnix: time.Now().Unix()}
	key := getEmbeddedPrivateKeyPEM()
	if key != "" {
		r.PrivateKeyLoaded = true
		if _, err := parseRSAPrivateKey(key); err == nil {
			r.PrivateKeyValid = true
		} else {
			r.LastError = "private key parse failed: " + err.Error()
		}
	}
	if sampleSecretToken != "" {
		if _, err := DecodeSecretPayload(sampleSecretToken); err == nil {
			r.CanDecodeSecret = true
		} else if r.LastError == "" {
			r.LastError = "decode secret failed: " + err.Error()
		}
	}
	b, err := json.Marshal(r)
	if err != nil {
		return `{"last_error":"self-check marshal failed"}`
	}
	return string(b)
}

