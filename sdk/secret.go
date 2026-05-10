package sdk

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"strings"
	"sync"
)

type SecretPayload struct {
	COSAppID   string `json:"cos_appid"`
	AppDomain  string `json:"app_domain"`
	AppName    string `json:"app_name"`
	SDKVersion string `json:"sdk_version"`
	AESKey     string `json:"AES-key"`
}

var (
	secretKeyMu           sync.RWMutex
	embeddedPrivateKeyPEM = ``
	EmbeddedPrivateKeyB64 = ""
)

type SDKConfigFile struct {
	PrivateKeyPEM string `json:"private_key_pem"`
}

func init() {
	if EmbeddedPrivateKeyB64 == "" {
		return
	}
	b, err := base64.StdEncoding.DecodeString(EmbeddedPrivateKeyB64)
	if err != nil {
		return
	}
	SetEmbeddedPrivateKeyPEM(string(b))
}

func SetEmbeddedPrivateKeyPEM(privateKeyPEM string) {
	secretKeyMu.Lock()
	embeddedPrivateKeyPEM = privateKeyPEM
	secretKeyMu.Unlock()
}

func LoadEmbeddedPrivateKeyFromConfigFile(path string) error {
	if path == "" {
		return fmt.Errorf("empty config file path")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read sdk config file: %w", err)
	}
	var cfg SDKConfigFile
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return fmt.Errorf("parse sdk config file json: %w", err)
	}
	if cfg.PrivateKeyPEM == "" {
		return fmt.Errorf("private_key_pem is empty in sdk config file")
	}
	SetEmbeddedPrivateKeyPEM(cfg.PrivateKeyPEM)
	return nil
}

func getEmbeddedPrivateKeyPEM() string {
	secretKeyMu.RLock()
	defer secretKeyMu.RUnlock()
	return embeddedPrivateKeyPEM
}

func DecodeSecretPayload(secret string) (*SecretPayload, error) {
	if p, ok := tryParsePlainSecretPayload(secret); ok {
		sdkDebugf("secret: decoded as plaintext JSON")
		return p, nil
	}
	privateKeyPEM := getEmbeddedPrivateKeyPEM()
	if privateKeyPEM == "" {
		sdkDebugf("secret: no embedded key, plaintext parse failed")
		return nil, fmt.Errorf("embedded private key is empty (plaintext parse also failed)")
	}
	sdkDebugf("secret: attempting RSA decrypt (embedded key present)")
	return decodeSecretPayloadWithKey(secret, privateKeyPEM)
}

func DecodeSecretPayloadWithKey(encryptedBase64, privateKeyPEM string) (*SecretPayload, error) {
	return decodeSecretPayloadWithKey(encryptedBase64, privateKeyPEM)
}

func decodeSecretPayloadWithKey(encryptedBase64, privateKeyPEM string) (*SecretPayload, error) {
	enc, err := base64.StdEncoding.DecodeString(encryptedBase64)
	if err != nil {
		return nil, fmt.Errorf("decode secret base64: %w", err)
	}
	key, err := parseRSAPrivateKey(privateKeyPEM)
	if err != nil {
		return nil, err
	}
	plain, err := rsa.DecryptPKCS1v15(rand.Reader, key, enc)
	if err != nil {
		plain, err = rsa.DecryptOAEP(sha256.New(), rand.Reader, key, enc, nil)
		if err != nil {
			return nil, fmt.Errorf("rsa decrypt failed: %w", err)
		}
	}
	var payload SecretPayload
	if err := json.Unmarshal(plain, &payload); err != nil {
		return nil, fmt.Errorf("parse decrypted payload json: %w", err)
	}
	if !isValidSecretPayload(&payload) {
		return nil, fmt.Errorf("invalid payload: required fields are missing")
	}
	return &payload, nil
}

func DecodeSecretPayloadJSON(secret string) (string, error) {
	payload, err := DecodeSecretPayload(secret)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}
	return string(b), nil
}

func DecodeSecretPayloadJSONWithKey(secret, privateKeyPEM string) (string, error) {
	payload, err := DecodeSecretPayloadWithKey(secret, privateKeyPEM)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}
	return string(b), nil
}

func parseRSAPrivateKey(privateKeyPEM string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(privateKeyPEM))
	if block == nil {
		return nil, fmt.Errorf("invalid private key pem")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse rsa private key: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("private key is not RSA")
	}
	return key, nil
}

func tryParsePlainSecretPayload(input string) (*SecretPayload, bool) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return nil, false
	}
	if strings.HasPrefix(trimmed, "{") {
		var p SecretPayload
		if err := json.Unmarshal([]byte(trimmed), &p); err == nil && isValidSecretPayload(&p) {
			return &p, true
		}
	}
	if decoded, err := base64.StdEncoding.DecodeString(trimmed); err == nil {
		var p SecretPayload
		if err := json.Unmarshal(decoded, &p); err == nil && isValidSecretPayload(&p) {
			return &p, true
		}
	}
	return nil, false
}

func isValidSecretPayload(p *SecretPayload) bool {
	return p != nil && p.AppName != "" && p.SDKVersion != "" && p.AESKey != ""
}

