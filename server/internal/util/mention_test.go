package util

import (
	"reflect"
	"testing"
)

func TestParseMentions(t *testing.T) {
	const (
		bruceID  = "416569d3-b193-4ada-9168-d0d3caf40b88"
		ultronID = "11111111-1111-1111-1111-111111111111"
		tonyID   = "22222222-2222-2222-2222-222222222222"
		mulID    = "679bf655-19f9-4373-9c4f-f90eee65f2d8"
		bobID    = "33333333-3333-3333-3333-333333333333"
	)

	tests := []struct {
		name string
		in   string
		want []Mention
	}{
		{
			name: "plain agent mention",
			in:   "[@Plain Name](mention://agent/" + bruceID + ") please review",
			want: []Mention{{Type: "agent", ID: bruceID}},
		},
		{
			name: "agent name contains [bracketed suffix]",
			in:   "[@QA Bruce [MF]](mention://agent/" + bruceID + ") please review",
			want: []Mention{{Type: "agent", ID: bruceID}},
		},
		{
			name: "two bracketed agent mentions in one comment",
			in:   "fyi [@CTO Ultron [MF]](mention://agent/" + ultronID + ") and [@Backend Tony [MF]](mention://agent/" + tonyID + ")",
			want: []Mention{
				{Type: "agent", ID: ultronID},
				{Type: "agent", ID: tonyID},
			},
		},
		{
			name: "issue mention adjacent to bracketed agent mention",
			in:   "see [MUL-155](mention://issue/" + mulID + ") and [@QA Bruce [MF]](mention://agent/" + bruceID + ")",
			want: []Mention{
				{Type: "issue", ID: mulID},
				{Type: "agent", ID: bruceID},
			},
		},
		{
			name: "member mention with brackets",
			in:   "[@Bob [Ops]](mention://member/" + bobID + ") take a look",
			want: []Mention{{Type: "member", ID: bobID}},
		},
		{
			name: "@all broadcast",
			in:   "[@All](mention://all/all) heads up",
			want: []Mention{{Type: "all", ID: "all"}},
		},
		{
			name: "duplicate mentions deduped",
			in:   "[@QA Bruce [MF]](mention://agent/" + bruceID + ") and again [@QA Bruce [MF]](mention://agent/" + bruceID + ")",
			want: []Mention{{Type: "agent", ID: bruceID}},
		},
		{
			name: "no mentions",
			in:   "just a plain comment with no mentions",
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseMentions(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseMentions(%q) =\n  %v\nwant:\n  %v", tt.in, got, tt.want)
			}
		})
	}
}
