package core

import (
	"reflect"
	"testing"
)

func Test_parseRequestBlock(t *testing.T) {
	type args struct {
		block string
	}
	tests := []struct {
		name    string
		args    args
		want    Request
		wantErr bool
	}{
		{
			name: "用例1",
			args: args{
				block: "GET https://www.baidu.com",
			},
			want: Request{
				Method:  "GET",
				URL:     "https://www.baidu.com",
				Headers: map[string]string{},
			},
			wantErr: false,
		},
		{
			name: "用例2",
			args: args{
				block: "GET https://www.baidu.com\nContent-Type: application/json",
			},
			want: Request{
				Method: "GET",
				URL:    "https://www.baidu.com",
				Headers: map[string]string{
					"Content-Type": "application/json",
				},
			},
			wantErr: false,
		},
		{
			name: "用例3",
			args: args{
				block: "Get https://www.baidu.com\nContent-Type:application/json",
			},
			want: Request{
				Method: "GET",
				URL:    "https://www.baidu.com",
				Headers: map[string]string{
					"Content-Type": "application/json",
				},
			},
			wantErr: false,
		},
		{
			name: "用例4",
			args: args{
				block: "POST https://www.baidu.com\nContent-Type:application/json\n\n{\n\"name\":\"gurl\"\n}",
			},
			want: Request{
				Method: "POST",
				URL:    "https://www.baidu.com",
				Headers: map[string]string{
					"Content-Type": "application/json",
				},
				Body: "{\n\"name\":\"gurl\"\n}",
			},
			wantErr: false,
		},
		{
			name: "用例5",
			args: args{
				block: "POST https://www.baidu.com\nContent-Type:application/json\n{\n\"name\":\"gurl\"\n}",
			},
			want: Request{
				Method: "POST",
				URL:    "https://www.baidu.com",
				Headers: map[string]string{
					"Content-Type": "application/json",
				},
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseRequestBlock(tt.args.block)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseRequestBlock() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseRequestBlock() = %v, want %v", got, tt.want)
			}
		})
	}
}
