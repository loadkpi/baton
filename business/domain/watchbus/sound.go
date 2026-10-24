package watchbus

import "context"

// Player plays the sound of a notification. Play returns at once: the sound
// is no reason to hold up the next notification or the next look.
type Player interface {
	Play()
}

// WithSound returns the Notifier that plays a sound after n has shown the
// message. A message that n could not show is not followed by a sound.
func WithSound(n Notifier, p Player) Notifier {
	return soundNotifier{notifier: n, player: p}
}

type soundNotifier struct {
	notifier Notifier
	player   Player
}

func (s soundNotifier) Notify(ctx context.Context, m Message) error {
	if err := s.notifier.Notify(ctx, m); err != nil {
		return err
	}
	s.player.Play()
	return nil
}
