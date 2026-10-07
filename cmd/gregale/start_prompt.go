package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type startPrompt struct {
	reader *bufio.Reader
	writer io.Writer
}

func (p *startPrompt) text(ctx context.Context, label, fallback string) (string, error) {
	if fallback == "" {
		_, _ = fmt.Fprintf(p.writer, "%s: ", label)
	} else {
		_, _ = fmt.Fprintf(p.writer, "%s [%s]: ", label, fallback)
	}
	// One active read at a time. Cancellation returns immediately; a blocked
	// terminal reader is abandoned only when the whole command is exiting.
	type answer struct {
		line string
		err  error
	}
	ch := make(chan answer, 1)
	go func() {
		line, err := p.reader.ReadString('\n')
		ch <- answer{line, err}
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case result := <-ch:
		if result.err != nil {
			return "", result.err // EOF never accepts a default or confirms a deploy.
		}
		value := strings.TrimSpace(result.line)
		if value == "" {
			value = fallback
		}
		return value, nil
	}
}

func (p *startPrompt) choose(ctx context.Context, label string, choices []string, fallback int) (int, error) {
	_, _ = fmt.Fprintf(p.writer, "\n%s\n", label)
	for i, choice := range choices {
		_, _ = fmt.Fprintf(p.writer, "  %d. %s\n", i+1, choice)
	}
	for {
		value, err := p.text(ctx, "Choose", strconv.Itoa(fallback+1))
		if err != nil {
			return 0, err
		}
		n, err := strconv.Atoi(value)
		if err == nil && n >= 1 && n <= len(choices) {
			return n - 1, nil
		}
		_, _ = fmt.Fprintf(p.writer, "Choose a number from 1 to %d.\n", len(choices))
	}
}

func (p *startPrompt) confirm(ctx context.Context, label string) (bool, error) {
	for {
		value, err := p.text(ctx, label+" (y/N)", "n")
		if err != nil {
			return false, err
		}
		switch strings.ToLower(value) {
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		default:
			_, _ = fmt.Fprintln(p.writer, "Enter y or n.")
		}
	}
}

func startInputExit(err error) int {
	if errors.Is(err, context.Canceled) {
		return 130
	}
	if errors.Is(err, io.EOF) {
		PrintProgress(osStdout, "Session closed. No further changes were submitted.")
		return 0
	}
	return printErr("Could not read session input", err)
}
