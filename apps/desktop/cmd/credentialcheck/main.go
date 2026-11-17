// credentialcheck qualifies the real OS credential store, without mocks or
// application credentials. Run it as the logged-in desktop user.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"

	"github.com/zalando/go-keyring"
)

const service = "ServerSendQualification"

func main() {
	mode := flag.String("mode", "roundtrip", "roundtrip, seed, expect-missing, expect-unavailable, cleanup, or child")
	flag.Parse()
	if err := run(*mode); err != nil {
		fmt.Fprintln(os.Stderr, runtime.GOOS+": FAIL:", err)
		os.Exit(1)
	}
	fmt.Println(runtime.GOOS+": PASS:", *mode)
}
func run(mode string) error {
	const key = "native-store-check-v1"
	switch mode {
	case "seed":
		value := make([]byte, 32)
		if _, err := rand.Read(value); err != nil {
			return err
		}
		return keyring.Set(service, key, hex.EncodeToString(value))
	case "roundtrip":
		if err := run("seed"); err != nil {
			return err
		}
		defer keyring.Delete(service, key)
		value, err := keyring.Get(service, key)
		if err != nil {
			return err
		}
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		command := exec.Command(exe, "-mode=child")
		command.Env = append(os.Environ(), "SERVERSEND_QUALIFICATION_VALUE="+value)
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		if err = command.Run(); err != nil {
			return err
		}
		if err = keyring.Delete(service, key); err != nil {
			return err
		}
		return run("expect-missing")
	case "child":
		value, err := keyring.Get(service, key)
		if err != nil {
			return err
		}
		if value != os.Getenv("SERVERSEND_QUALIFICATION_VALUE") || len(value) != 64 {
			return errors.New("secret changed across process restart")
		}
		return nil
	case "expect-missing":
		if _, err := keyring.Get(service, key); !errors.Is(err, keyring.ErrNotFound) {
			return errors.New("expected a missing item after deletion/loss")
		}
		return nil
	case "expect-unavailable":
		// After seeding, manually lock the store or stop its service before running.
		// Any unlock prompt must be cancelled to exercise the failure path.
		if _, err := keyring.Get(service, key); err == nil || errors.Is(err, keyring.ErrNotFound) {
			return errors.New("expected locked/unavailable store failure")
		}
		if err := keyring.Set(service, key, "qualification-only"); err == nil {
			return errors.New("write unexpectedly succeeded while unavailable")
		}
		return nil
	case "cleanup":
		err := keyring.Delete(service, key)
		if errors.Is(err, keyring.ErrNotFound) {
			return nil
		}
		return err
	default:
		return errors.New("unknown qualification mode")
	}
}
