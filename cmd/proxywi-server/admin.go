package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/butialabs/proxywi/internal/config"
	"github.com/butialabs/proxywi/internal/storage"
	"golang.org/x/crypto/bcrypt"
)

func runAdminSet(args []string) error {
	fs := flag.NewFlagSet("admin-set", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `usage: proxywi-server admin-set [flags]

Rotate the credentials of an existing admin. Pass only the fields you want
to change.

flags:
  -target    current username (optional if there is exactly one admin)
  -username  new username
  -email     new email address
  -password  new password (will be bcrypt-hashed)

The database is read from PROXYWI_DATA_DIR (default: ./data).
`)
	}
	target := fs.String("target", "", "current admin username to update (optional if only one admin exists)")
	newUser := fs.String("username", "", "new username")
	newEmail := fs.String("email", "", "new email")
	newPass := fs.String("password", "", "new password")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *newUser == "" && *newEmail == "" && *newPass == "" {
		fs.Usage()
		return errors.New("nothing to change: provide at least one of -username, -email, -password")
	}

	cfg, err := config.LoadServer()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	store, err := storage.Open(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer store.Close()

	ctx := context.Background()

	var admin *storage.Admin
	if *target != "" {
		admin, err = store.AdminByUsername(ctx, *target)
		if err != nil {
			return err
		}
		if admin == nil {
			return fmt.Errorf("no admin with username %q", *target)
		}
	} else {
		admins, err := store.ListAdmins(ctx)
		if err != nil {
			return err
		}
		switch len(admins) {
		case 0:
			return errors.New("no admin exists yet, create one via the GUI first")
		case 1:
			a := admins[0]
			admin = &a
		default:
			names := make([]string, 0, len(admins))
			for _, a := range admins {
				names = append(names, a.Username)
			}
			return fmt.Errorf("multiple admins exist (%s); use -target to pick one", strings.Join(names, ", "))
		}
	}

	var passHash string
	if *newPass != "" {
		if len(*newPass) < 8 {
			return errors.New("password must be at least 8 characters")
		}
		h, err := bcrypt.GenerateFromPassword([]byte(*newPass), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		passHash = string(h)
	}

	if err := store.UpdateAdmin(ctx, admin.ID, *newUser, *newEmail, passHash); err != nil {
		return fmt.Errorf("update admin: %w", err)
	}

	fmt.Printf("updated admin id=%d (was %q)\n", admin.ID, admin.Username)
	if *newUser != "" {
		fmt.Printf("  username → %s\n", *newUser)
	}
	if *newEmail != "" {
		fmt.Printf("  email    → %s\n", *newEmail)
	}
	if *newPass != "" {
		fmt.Printf("  password updated (bcrypt)\n")
	}
	return nil
}
