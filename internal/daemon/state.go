package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/giapnguyen74/uvpm/internal/home"
	"github.com/giapnguyen74/uvpm/internal/model"
	"github.com/giapnguyen74/uvpm/internal/version"
)

type persistedApp struct {
	ID         int        `json:"id"`
	Spec       model.Spec `json:"spec"`
	Desired    string     `json:"desired"`
	Status     string     `json:"status"`
	Pid        int        `json:"pid,omitempty"`
	StartTicks uint64     `json:"start_ticks,omitempty"` // pid-reuse guard
	StartedAt  time.Time  `json:"started_at,omitempty"`
	Restarts   int        `json:"restarts"`
	SyncHash   string     `json:"sync_hash,omitempty"`
	LastExit   string     `json:"last_exit,omitempty"`
}

type stateFile struct {
	Schema int            `json:"schema"`
	NextID int            `json:"next_id"`
	Apps   []persistedApp `json:"apps"`
}

func writeJSONAtomic(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readState(path string) (*stateFile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var st stateFile
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if st.Schema > version.StateSchema {
		return nil, fmt.Errorf("%s: schema %d is newer than this uvpm supports (%d)", path, st.Schema, version.StateSchema)
	}
	// Older schemas would be migrated here.
	return &st, nil
}

func stateFilePath() string { return home.StateFile() }
