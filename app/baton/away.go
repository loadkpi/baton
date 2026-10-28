package main

import (
	"context"
	"path/filepath"

	"github.com/loadkpi/baton/business/domain/awaybus"
	"github.com/loadkpi/baton/business/domain/awaybus/stores/awayfile"
	"github.com/loadkpi/baton/business/domain/configbus"
	"github.com/loadkpi/baton/business/domain/configbus/stores/configfile"
	"github.com/loadkpi/baton/business/domain/watchbus/stores/telegram"
)

// runAway implements `baton away`: from now on `baton watch` sends its
// notifications to Telegram instead of the desktop, until `baton back`.
func runAway(ctx context.Context, c console, home string, args []string) int {
	if len(args) != 0 {
		return c.usagef("away: %s", awayUsage)
	}
	// Going away without a working Telegram would silence the notifications
	// the user is relying on, so the settings must be there first.
	if _, err := newTelegramBus(home).TelegramSettings(ctx); err != nil {
		return c.errorf("away: %v", err)
	}
	if err := awaybus.New(awayfile.New(filepath.Join(home, ".baton"))).Away(ctx); err != nil {
		return c.errorf("away: %v", err)
	}
	c.println("away: notifications go to Telegram until baton back")
	return exitOK
}

// runBack implements `baton back`: notifications return to the desktop.
func runBack(ctx context.Context, c console, home string, args []string) int {
	if len(args) != 0 {
		return c.usagef("back: %s", backUsage)
	}
	if err := awaybus.New(awayfile.New(filepath.Join(home, ".baton"))).Back(ctx); err != nil {
		return c.errorf("back: %v", err)
	}
	c.println("back: notifications go to the desktop")
	return exitOK
}

// newTelegramBus is the configuration domain over the files in home.
func newTelegramBus(home string) *configbus.Bus {
	return configbus.New(configfile.New(filepath.Join(home, ".config", "baton", "config.toml"), home))
}

// telegramSource is where the Telegram notifier finds its settings: the
// config and the secrets file, read for every message.
func telegramSource(home string) telegram.Source {
	bus := newTelegramBus(home)
	return func(ctx context.Context) (telegram.Settings, error) {
		s, err := bus.TelegramSettings(ctx)
		if err != nil {
			return telegram.Settings{}, err
		}
		return toStoreTelegramSettings(s), nil
	}
}

func toStoreTelegramSettings(s configbus.TelegramSettings) telegram.Settings {
	return telegram.Settings{APIURL: s.APIURL, ChatID: s.ChatID, Token: s.Token}
}
