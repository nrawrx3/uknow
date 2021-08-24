package main

import (
	"fmt"
	"log"
	"os"
)

func createFileLogger(setAsDefault bool) *log.Logger {
	fileName := fmt.Sprintf("/tmp/%s_log.txt", localPlayerName)
	f, err := os.OpenFile(fileName, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0666)
	if err != nil {
		log.Fatalf("Failed to open/create log file: %s", fileName)
	}

	if setAsDefault {
		log.SetOutput(f)
		return log.Default()
	} else {
		return log.New(f, localPlayerName, log.Ltime|log.Lshortfile)
	}
}

type ConsoleLog struct {
	c *Console
}

func (clog *ConsoleLog) Write(bytes []byte) (int, error) {
	clog.c.appendEventLog(string(bytes[:]))
	return len(bytes), nil
}
