package api

import "errors"

type RetentionPolicy struct {
	TrashDays   int `json:"trash_days"`
	VersionDays int `json:"version_days"`
}

func (p RetentionPolicy) Validate() error {
	if p.TrashDays < 1 || p.TrashDays > 36500 || p.VersionDays < 0 || p.VersionDays > 36500 {
		return errors.New("invalid retention days")
	}
	return nil
}
