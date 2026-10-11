// 实时明细（live）内存缓存：每次上报刷新，详情接口读取。
package api

import "encoding/json"

// SetLive 供采集引擎回调：缓存最新实时明细（SSH/PVE/群晖/拉取 路径共用）。
func (s *Server) SetLive(nodeID int64, data json.RawMessage) {
	s.setLive(nodeID, data)
}

func (s *Server) setLive(nodeID int64, data json.RawMessage) {
	s.livesMu.Lock()
	s.lives[nodeID] = data
	s.livesMu.Unlock()
}

func (s *Server) getLive(nodeID int64) json.RawMessage {
	s.livesMu.RLock()
	defer s.livesMu.RUnlock()
	return s.lives[nodeID]
}
