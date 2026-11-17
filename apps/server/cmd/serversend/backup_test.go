package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestBackupCommandRefusesOtherModesBeforeAccessingStorage(t *testing.T) {
	for _, args := range [][]string{
		{"-backup", "/backup", "-check"},
		{"-backup", "/backup", "-bootstrap-admin", "admin"},
		{"-backup", "/backup", "-recover-bootstrap-admin", "admin"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var output bytes.Buffer
			err := run(t.Context(), args, &output)
			if err == nil || !strings.Contains(err.Error(), "use only one") || output.Len() != 0 {
				t.Fatalf("conflicting backup mode: error=%v, output=%q", err, output.String())
			}
		})
	}
}
