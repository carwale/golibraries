package connection

import (
	"testing"

	"github.com/carwale/golibraries/gologger"

	"github.com/streadway/amqp"
)

func TestProvider_NewConnection(t *testing.T) {

	type args struct {
		server   string
		username string
		password string
	}
	tests := []struct {
		name     string
		provider *Provider
		args     args
		want     *amqp.Connection
		wantErr  bool
	}{
		{
			"test whether connection can be established",
			&Provider{},
			args{
				"172.16.0.11:5672",
				"guest",
				"guest",
			},
			nil,
			false,
		},
		{
			"test whether error occurs in case of invalid server string",
			&Provider{},
			args{
				"172.16.2.79",
				"guest",
				"guest",
			},
			nil,
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.provider.NewConnection(tt.args.server, tt.args.username, tt.args.password, gologger.NewLogger())
			if (err != nil) != tt.wantErr {
				t.Errorf("Provider.NewConnection() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			// if !reflect.DeepEqual(got, tt.want) {
			// 	t.Errorf("Provider.NewConnection() = %v, want %v", got, tt.want)
			// }
		})
	}
}
