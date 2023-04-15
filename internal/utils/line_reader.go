package utils

import (
	"bufio"
	"errors"
	"io"
	"log"
)

var ErrDoneReadingLines = errors.New("done reading lines")

type LineReader struct {
	scanner           *bufio.Scanner
	remainingPrevLine []byte
	logger            *log.Logger
}

// Creates a new LineReader that reads from the given io.Reader.
func NewLineReader(r io.Reader, logger *log.Logger) *LineReader {
	return &LineReader{
		scanner: bufio.NewScanner(r),
		logger:  logger,
	}
}

// Read implements the io.Reader interface. It reads a line from the underlying
// io.Reader and returns io.EOF after each line. If/when the underlying reader
// returns io.EOF, this method returns ErrDoneReadingLines.
func (reader *LineReader) Read(p []byte) (n int, err error) {
	if len(reader.remainingPrevLine) > 0 {
		return reader.flushCurrentLine(p)
	}

	if reader.scanner.Scan() {
		reader.remainingPrevLine = reader.scanner.Bytes()
		if len(reader.remainingPrevLine) == 0 {
			return reader.Read(p)
		}
		reader.logger.Printf("readerBytes: %s", reader.remainingPrevLine)
		return reader.flushCurrentLine(p)
	}

	if reader.scanner.Err() != nil {
		return 0, reader.scanner.Err()
	}
	return 0, ErrDoneReadingLines
}

func (reader *LineReader) flushCurrentLine(p []byte) (n int, err error) {
	bytesCopied := copy(p, reader.remainingPrevLine)
	reader.logger.Printf("flushing [%s], copied upto [%s]", reader.remainingPrevLine, p)
	if bytesCopied < len(reader.remainingPrevLine) {
		reader.logger.Printf("should not usually be here: bytesCopied: %d, remainingPrevLine: %d", bytesCopied, len(reader.remainingPrevLine))
		reader.remainingPrevLine = reader.remainingPrevLine[bytesCopied:]
		return bytesCopied, nil
	}
	reader.remainingPrevLine = nil
	return bytesCopied, io.EOF
}
