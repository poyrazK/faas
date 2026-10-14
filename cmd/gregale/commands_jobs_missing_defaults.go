package main

import (
	"errors"
	"fmt"
	"os"
)

type missingSavedJobError struct {
	path    string
	example []string
}

func (err *missingSavedJobError) Error() string {
	return fmt.Sprintf("no Job is saved for this checkout. Defaults path: %s. Provide a Job name, for example: %s. Or save one with: %s. Replace my-job with your Job name; these are examples, and no command was executed. Run gregale jobs --help for available commands", oneLine(err.path), savedNameSuggestionCommand(err.example), savedNameSuggestionCommand([]string{"jobs", "defaults", "set", "my-job"}))
}

func readJobDefaultsForCommand(example []string) (localJobDefaults, string, error) {
	defaults, path, err := readJobDefaults()
	if path != "" && errors.Is(err, os.ErrNotExist) {
		err = &missingSavedJobError{path: path, example: example}
	}
	return defaults, path, err
}

func printJobDefaultsSelectionError(title string, err error) int {
	var missing *missingSavedJobError
	if errors.As(err, &missing) {
		title = "No saved Job"
	}
	return printErr(title, err)
}
