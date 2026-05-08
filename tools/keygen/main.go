package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"flag"
	"fmt"
	"os"
)

func main() {
	privateOut := flag.String("private-out", "config-templates/private_key.pem", "private key output path")
	publicOut := flag.String("public-out", "config-templates/public_key.pem", "public key output path")
	bits := flag.Int("bits", 2048, "RSA key bits")
	flag.Parse()
	key, err := rsa.GenerateKey(rand.Reader, *bits)
	if err != nil {
		fail("generate rsa key: %v", err)
	}
	priv := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		fail("marshal public key: %v", err)
	}
	pub := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})
	if err := os.WriteFile(*privateOut, priv, 0o600); err != nil {
		fail("write private key: %v", err)
	}
	if err := os.WriteFile(*publicOut, pub, 0o644); err != nil {
		fail("write public key: %v", err)
	}
	fmt.Printf("private: %s\npublic: %s\n", *privateOut, *publicOut)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

