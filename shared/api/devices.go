package api

import (
	"time"

	"github.com/chironjit/serversend/shared/protocol"
)

type DeviceInfo struct {
	Current          bool        `json:"current"`
	ID               protocol.ID `json:"id"`
	Name             string      `json:"name"`
	Platform         string      `json:"platform"`
	AppVersion       string      `json:"app_version"`
	SignedInAt       time.Time   `json:"signed_in_at"`
	LastSeenAt       time.Time   `json:"last_seen_at"`
	LastRefreshAt    *time.Time  `json:"last_refresh_at,omitempty"`
	IdleExpiresAt    *time.Time  `json:"idle_expires_at,omitempty"`
	ReauthenticateAt *time.Time  `json:"reauthenticate_at,omitempty"`
	RevokedAt        *time.Time  `json:"revoked_at,omitempty"`
	Status           string      `json:"status"`
	RecentlyActive   bool        `json:"recently_active"`
}
