package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/starcs/star-launcher-backend/internal/domain"
	"github.com/starcs/star-launcher-backend/internal/mysqlrepo"
)

type taskRepository interface {
	Tasks(ctx context.Context, steamID uint64) (domain.TaskCenterOverview, error)
}

func (h *Handler) handleTasks(w http.ResponseWriter, r *http.Request) {
	if !h.requireGET(w, r) {
		return
	}
	steamID, token, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	if !h.verifyOperationPassword(w, r, steamID, token) {
		return
	}
	repository, ok := h.players.(taskRepository)
	if !ok {
		h.writeError(w, http.StatusServiceUnavailable, 5003, "任务系统尚未配置")
		return
	}
	overview, err := repository.Tasks(r.Context(), steamID)
	if err != nil {
		if errors.Is(err, mysqlrepo.ErrTaskSchemaUnavailable) {
			h.logger.Warn("task schema is unavailable", "error", err)
			h.writeError(w, http.StatusServiceUnavailable, 5003, "任务系统尚未启用")
			return
		}
		h.logger.Error("query player tasks", "error", err)
		h.writeError(w, http.StatusServiceUnavailable, 5002, "读取任务数据失败")
		return
	}
	h.writeSuccess(w, overview)
}
