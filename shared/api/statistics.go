package api

import "time"

// StorageStatistics separates file allocations from logical content and promises.
// ReservedBytes are not allocated bytes and must not be added to disk usage.
type StorageStatistics struct {
	PrivateCiphertextBytes int64                `json:"private_ciphertext_bytes"`
	AvailableBytes         int64                `json:"available_bytes"`
	UpdatedAt              time.Time            `json:"updated_at"`
	LogicalBytes           int64                `json:"logical_bytes"`
	CommittedBytes         int64                `json:"committed_bytes"`
	RetainedBytes          int64                `json:"retained_bytes"`
	StagedBytes            int64                `json:"staged_bytes"`
	TrashBytes             int64                `json:"trash_bytes"`
	OtherBytes             int64                `json:"other_bytes"`
	ReservedBytes          int64                `json:"reserved_bytes"`
	UnwrittenBytes         int64                `json:"unwritten_bytes"`
	QuotaBytes             int64                `json:"quota_bytes"`
	ActiveUploads          int64                `json:"active_uploads"`
	Filesystems            []FilesystemCapacity `json:"filesystems,omitempty"`
	Transfer               TransferStatistics   `json:"transfer"`
}
type FilesystemCapacity struct {
	ID             string   `json:"id"`
	Roles          []string `json:"roles"`
	AvailableBytes int64    `json:"available_bytes"`
	TotalBytes     int64    `json:"total_bytes"`
}

// Wire counts application payload, excluding TLS/HTTP framing. Useful upload
// bytes count first durable acknowledgements; retries include rejected payload.
type TransferStatistics struct {
	WireBytes   int64     `json:"wire_bytes"`
	UsefulBytes int64     `json:"useful_bytes"`
	RetryBytes  int64     `json:"retry_bytes"`
	WireRate    float64   `json:"wire_rate"`
	UsefulRate  float64   `json:"useful_rate"`
	RetryRate   float64   `json:"retry_rate"`
	UpdatedAt   time.Time `json:"updated_at"`
}
