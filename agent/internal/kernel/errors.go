package kernel

import "errors"

var (
	ErrInterfaceNotFound = errors.New("interface not found")
	ErrPeerNotFound      = errors.New("peer not found")
	ErrIPSetNotFound     = errors.New("ipset not found")
	ErrInvalidName       = errors.New("invalid name")
	ErrInvalidKey        = errors.New("invalid public key")
)
