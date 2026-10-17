//go:build !inbuiltble
// +build !inbuiltble

package botsetup

import "github.com/julianvidalm/wire-pod/chipper/pkg/logger"

func RegisterBLEAPI() {
	logger.Println("BLE API is unregistered")
}
