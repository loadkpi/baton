// Package soundplay plays the sound of a desktop notification with pw-play,
// or else paplay, found on PATH. Nothing here asks the desktop whether it
// wants a sound: the user asked for one in the config.
package soundplay

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/loadkpi/baton/business/types/sound"
)

// timeout is how long a player may run before it is killed, so that a sound
// server that does not answer cannot pile up processes.
const timeout = 10 * time.Second

// players are the programs that play a file, in the order they are tried.
var players = []string{"pw-play", "paplay"}

// extensions are the files a name can stand for, in the order they are tried.
var extensions = []string{"oga", "ogg", "wav"}

// Player plays one sound file.
type Player struct {
	file string
	// program is the player found on PATH when the Player was made; empty when
	// there is none.
	program string
	warn    func(string)

	mu     sync.Mutex
	warned map[string]bool
}

// New finds the file of a sound (a name in the sound theme or a path, already
// validated by the sound package) and the player to run. warn gets a line for
// a player that fails or hangs, once for each distinct line, and may be called
// from another goroutine. The error says where the file was looked for and
// does not repeat the sound.
func New(home, snd string, warn func(string)) (*Player, error) {
	file, err := resolve(home, snd)
	if err != nil {
		return nil, err
	}
	p := &Player{file: file, warn: warn, warned: map[string]bool{}}
	for _, name := range players {
		if _, err := exec.LookPath(name); err == nil {
			p.program = name
			break
		}
	}
	return p, nil
}

// Problem says why nothing will be played, or is empty.
func (p *Player) Problem() string {
	if p.program != "" {
		return ""
	}
	return fmt.Sprintf("sound: neither %s is on PATH; notifications will have no sound", strings.Join(players, " nor "))
}

// Play starts the player for the file in the background and returns. A player
// that fails, or runs longer than the limit and is killed, is noted with one
// line (see New). Sounds that overlap are not queued.
func (p *Player) Play() {
	if p.program == "" {
		return
	}
	go p.run()
}

func (p *Player) run() {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, p.program, p.file)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	// A player that left children behind must not keep the pipe, and with it
	// this goroutine, for ever.
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	switch {
	case err == nil:
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		p.note(fmt.Sprintf("sound: %s did not finish in %s and was killed", p.program, timeout))
	default:
		msg := fmt.Sprintf("sound: %s: %v", p.program, err)
		if first, _, _ := strings.Cut(strings.TrimSpace(stderr.String()), "\n"); first != "" {
			msg += ": " + first
		}
		p.note(msg)
	}
}

// note passes on a line the first time it is seen.
func (p *Player) note(msg string) {
	p.mu.Lock()
	seen := p.warned[msg]
	p.warned[msg] = true
	p.mu.Unlock()
	if !seen {
		p.warn(msg)
	}
}

// searchDirs are the sound directories, the user's first so that a theme in
// the home directory wins.
func searchDirs(home string) []string {
	return []string{filepath.Join(home, ".local", "share", "sounds"), "/usr/local/share/sounds", "/usr/share/sounds"}
}

// resolve returns the file a name or a path stands for.
func resolve(home, snd string) (string, error) {
	if sound.IsPath(snd) {
		file := snd
		if strings.HasPrefix(file, "~/") {
			file = filepath.Join(home, file[2:])
		}
		if info, err := os.Stat(file); err != nil || !info.Mode().IsRegular() {
			return "", errors.New("the sound path is not a regular file")
		}
		return file, nil
	}
	dirs := searchDirs(home)
	for _, dir := range dirs {
		for _, ext := range extensions {
			file := filepath.Join(dir, "freedesktop", "stereo", snd+"."+ext)
			if info, err := os.Stat(file); err == nil && info.Mode().IsRegular() {
				return file, nil
			}
		}
	}
	return "", fmt.Errorf("no sound file of that name (%s) in freedesktop/stereo of %s", strings.Join(extensions, ", "), strings.Join(dirs, ", "))
}
