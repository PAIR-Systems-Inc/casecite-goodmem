//go:build !unix

package app

import "errors"

func lockGoodMemSetup(string) (func(), error) {
	return nil, errors.New("run GoodMem provisioning with the setup container or on a Unix host; the runtime can use the generated configuration on this host")
}
