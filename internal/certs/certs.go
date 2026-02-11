package certs

import (
	_ "embed"
)

//go:embed ca.crt
var CACert []byte

//go:embed server.crt
var ServerCert []byte

//go:embed server.key
var ServerKey []byte
