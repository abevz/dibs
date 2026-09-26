package main

import (
	"encoding/json"
	"fmt"
)

type completionMarker struct {
	PRURL     string `json:"pr_url"`
	CommitSHA string `json:"commit_sha"`
	Branch    string `json:"branch"`
	Note      string `json:"note"`
}

func parseCompletionMarker(data []byte) (completionMarker, error) {
	if string(data) == "done\n" {
		return completionMarker{}, nil
	}
	var marker completionMarker
	if err := json.Unmarshal(data, &marker); err != nil {
		return completionMarker{}, fmt.Errorf("invalid completion marker: %w", err)
	}
	return marker, nil
}
