package channels

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"sync"

	"ns-apns/internal/forward"
)

var ledgerMu sync.Mutex

type deliveryState struct {
	Statuses  map[string]Status `json:"statuses"`
	Delivered []string          `json:"delivered"`
}

func (s *Dispatcher) LoadState(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ledgerMu.Lock()
	defer ledgerMu.Unlock()
	s.statePath = path
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("无法读取推送状态文件，请检查工作目录权限")
	}
	var state deliveryState
	if len(data) > 1<<20 || json.Unmarshal(data, &state) != nil {
		return fmt.Errorf("推送状态文件格式有误，请备份后检查")
	}
	if state.Statuses != nil {
		s.statuses = state.Statuses
	}
	s.delivered = state.Delivered
	if len(s.delivered) > 512 {
		s.delivered = s.delivered[len(s.delivered)-512:]
	}
	return nil
}
func deliveryKey(name string, ev forward.Event) string {
	return name + "/" + ev.Source + "/" + strconv.Itoa(ev.TelegramMessageID)
}
func (s *Dispatcher) wasDelivered(name string, ev forward.Event) bool {
	if ev.TelegramMessageID == 0 {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := deliveryKey(name, ev)
	for _, seen := range s.delivered {
		if seen == key {
			return true
		}
	}
	return false
}
func (s *Dispatcher) persistLocked() error {
	if s.statePath == "" {
		return nil
	}
	ledgerMu.Lock()
	defer ledgerMu.Unlock()
	if raw, readErr := os.ReadFile(s.statePath); readErr == nil {
		var saved deliveryState
		if len(raw) > 1<<20 || json.Unmarshal(raw, &saved) != nil {
			return fmt.Errorf("投递状态格式有误")
		}
		for name, state := range saved.Statuses {
			if state.LastAttempt.After(s.statuses[name].LastAttempt) {
				s.statuses[name] = state
			}
		}
		combined := append(saved.Delivered, s.delivered...)
		seen := map[string]bool{}
		unique := []string{}
		for _, key := range combined {
			if !seen[key] {
				unique = append(unique, key)
				seen[key] = true
			}
		}
		if len(unique) > 512 {
			unique = unique[len(unique)-512:]
		}
		s.delivered = unique
	} else if !os.IsNotExist(readErr) {
		return readErr
	}
	data, err := json.Marshal(deliveryState{s.statuses, s.delivered})
	if err != nil {
		return err
	}
	tmp := s.statePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, s.statePath)
}
