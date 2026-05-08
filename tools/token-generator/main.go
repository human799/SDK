package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"os"
	"strings"
)

type payload struct {
	COSAppID   string `json:"cos_appid"`
	AppDomain  string `json:"app_domain"`
	AppName    string `json:"app_name"`
	SDKVersion string `json:"sdk_version"`
	AESKey     string `json:"AES-key"`
}

func main() {
	publicKey := flag.String("public-key", "", "RSA public key PEM file path")
	payloadPath := flag.String("payload", "", "JSON payload file path")
	mode := flag.String("mode", "pkcs1v15", "pkcs1v15 or oaep-sha256")
	out := flag.String("out", "", "output token file path")
	flag.Parse()
	if *publicKey == "" || *payloadPath == "" {
		fail("usage: go run ./tools/token-generator -public-key pub.pem -payload payload.json [-mode pkcs1v15|oaep-sha256] [-out token.txt]")
	}
	pub, err := loadPub(*publicKey)
	if err != nil {
		fail("load public key: %v", err)
	}
	raw, err := os.ReadFile(*payloadPath)
	if err != nil {
		fail("read payload: %v", err)
	}
	if err := validate(raw); err != nil {
		fail("payload validate failed: %v", err)
	}
	token, err := encrypt(pub, raw, *mode)
	if err != nil {
		fail("encrypt token failed: %v", err)
	}
	if *out != "" {
		_ = os.WriteFile(*out, []byte(token), 0o600)
	}
	fmt.Println(token)
}

func loadPub(path string) (*rsa.PublicKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("invalid PEM")
	}
	if k, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		if r, ok := k.(*rsa.PublicKey); ok {
			return r, nil
		}
	}
	if cert, err := x509.ParseCertificate(block.Bytes); err == nil {
		if r, ok := cert.PublicKey.(*rsa.PublicKey); ok {
			return r, nil
		}
	}
	return nil, fmt.Errorf("unsupported RSA public key format")
}
func validate(raw []byte) error {
	var p payload
	if err := json.Unmarshal(raw, &p); err != nil {
		return err
	}
	if p.COSAppID == "" || p.AppDomain == "" || p.AppName == "" || p.SDKVersion == "" || p.AESKey == "" {
		return fmt.Errorf("missing required fields")
	}
	return nil
}
func encrypt(pub *rsa.PublicKey, payload []byte, mode string) (string, error) {
	var (
		enc []byte
		err error
	)
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "pkcs1v15":
		enc, err = rsa.EncryptPKCS1v15(rand.Reader, pub, payload)
	case "oaep-sha256":
		enc, err = rsa.EncryptOAEP(sha256.New(), rand.Reader, pub, payload, nil)
	default:
		return "", fmt.Errorf("unsupported mode: %s", mode)
	}
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(enc), nil
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

