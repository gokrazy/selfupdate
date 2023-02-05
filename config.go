package main

import (
	"io/ioutil"
	"os"
	"strings"
)

// readConfigFile reads configuration files from /perm /etc or / and returns
// trimmed content as string.
//
// TODO: de-duplicate this with gokrazy.go into a gokrazy public package.
func readConfigFile(fileName string) (string, error) {
	str, err := ioutil.ReadFile("/perm/" + fileName)
	if err != nil {
		str, err = ioutil.ReadFile("/etc/" + fileName)
	}
	if err != nil && os.IsNotExist(err) {
		str, err = ioutil.ReadFile("/" + fileName)
	}

	return strings.TrimSpace(string(str)), err
}
