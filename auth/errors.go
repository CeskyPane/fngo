package auth

import (
	"errors"
)

var ErrNoToken = errors.New("auth: no token in store")

type fatalMarker interface {
	Fatal() bool
}

func IsFatal(err error) bool {
	if err == nil {
		return false
	}

	var marker fatalMarker
	if !errors.As(err, &marker) {
		return false
	}

	return marker.Fatal()
}
