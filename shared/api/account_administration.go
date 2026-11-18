package api

import (
	"time"

	"github.com/chironjit/serversend/shared/protocol"
)

type RecoveryCodeReplacement struct {
	Password string `json:"password"`
	Code     string `json:"code"`
}

type AccountAuditEvent struct {
	ID        int64       `json:"id,string"`
	Actor     string      `json:"actor"`
	Target    string      `json:"target"`
	Action    string      `json:"action"`
	DeviceID  protocol.ID `json:"device_id,omitempty"`
	CreatedAt time.Time   `json:"created_at"`
}

type AccountAuditPage struct {
	Events     []AccountAuditEvent `json:"events"`
	NextBefore int64               `json:"next_before,string,omitempty"`
}
