package bus

import "testing"

func TestMessageValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		message Message
		max     int
		wantErr bool
	}{
		{name: "valid", message: Message{ID: "outbox-1", Subject: "statusmon.events.normal", Data: []byte(`{}`)}, max: 1024},
		{name: "missing ID", message: Message{Subject: "statusmon.events.normal", Data: []byte(`{}`)}, max: 1024, wantErr: true},
		{name: "wildcard", message: Message{ID: "1", Subject: "statusmon.events.*", Data: []byte(`{}`)}, max: 1024, wantErr: true},
		{name: "empty token", message: Message{ID: "1", Subject: "statusmon..normal", Data: []byte(`{}`)}, max: 1024, wantErr: true},
		{name: "empty data", message: Message{ID: "1", Subject: "statusmon.events.normal"}, max: 1024, wantErr: true},
		{name: "too large", message: Message{ID: "1", Subject: "statusmon.events.normal", Data: []byte(`123`)}, max: 2, wantErr: true},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := test.message.Validate(test.max)
			if (err != nil) != test.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}
