package consumer

import (
	"errors"
	"fmt"
	"testing"

	"github.com/ledatu/csar-notify/internal/domain"
	"github.com/ledatu/csar-notify/internal/provider"
)

func TestShouldAckAfterDispatchError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "permanent web push only",
			err:  fmt.Errorf("dispatch failed: %w", provider.PermanentSendError(domain.ChannelWebPush, errors.New("bad jwt"))),
			want: true,
		},
		{
			name: "temporary web push error",
			err:  fmt.Errorf("dispatch failed: %w", provider.TemporarySendError(domain.ChannelWebPush, errors.New("timeout"))),
			want: false,
		},
		{
			name: "mixed permanent channels",
			err: errors.Join(
				fmt.Errorf("dispatch failed: %w", provider.PermanentSendError(domain.ChannelWebPush, errors.New("bad jwt"))),
				fmt.Errorf("dispatch failed: %w", provider.PermanentSendError(domain.ChannelTelegram, errors.New("telegram failed"))),
			),
			want: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := shouldAckAfterDispatchError(tt.err); got != tt.want {
				t.Fatalf("shouldAckAfterDispatchError() = %v, want %v", got, tt.want)
			}
		})
	}
}
