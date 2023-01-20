package main

import (
	"fmt"
	"os"
)

func mne(err error, identifier string) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERR:"+identifier)
		panic(err)
	}
}
