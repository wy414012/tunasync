package internal

import (
	"encoding/json"
	"errors"
	"fmt"
)

type SyncStatus uint8

const (
	None SyncStatus = iota
	Failed
	Success
	Syncing
	PreSyncing
	Paused
	Disabled
)

func (s SyncStatus) String() string {
	switch s {
	case None:
		return "none"
	case Failed:
		return "failed"
	case Success:
		return "success"
	case Syncing:
		return "syncing"
	case PreSyncing:
		return "pre-syncing"
	case Paused:
		return "paused"
	case Disabled:
		return "disabled"
	default:
		return ""
	}
}

func (s SyncStatus) MarshalJSON() ([]byte, error) {
	strStatus := s.String()
	if strStatus == "" {
		return []byte{}, errors.New("无效的状态值")
	}

	return json.Marshal(strStatus)
}

func (s *SyncStatus) UnmarshalJSON(v []byte) error {
	sv := string(v)
	switch sv {
	case `"none"`:
		*s = None
	case `"failed"`:
		*s = Failed
	case `"success"`:
		*s = Success
	case `"syncing"`:
		*s = Syncing
	case `"pre-syncing"`:
		*s = PreSyncing
	case `"paused"`:
		*s = Paused
	case `"disabled"`:
		*s = Disabled
	default:
		return fmt.Errorf("无效的状态值: %s", string(v))
	}
	return nil
}
