// Copyright 2019 VMware, Inc.
// SPDX-License-Identifier: Apache-2.0

package upgrade

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestEtcdMemberHealthStructDecoding(t *testing.T) {
	data := `{
		"header": {
			"cluster_id":14841639068965178418,
			"member_id":10276657743932975437,
			"raft_term":444
		},
		"members": [
			{
				"ID":5782640540428238474,
				"name":"two",
				"peerURLs":["http://localhost:3380"],
				"clientURLs":["http://localhost:3379"]
			},
			{
				"ID":10276657743932975437,
				"name":"default",
				"peerURLs":["http://localhost:2380"],
				"clientURLs":["http://localhost:2379"]
			}
		]
	}`

	var r etcdMembersResponse

	if err := json.Unmarshal([]byte(data), &r); err != nil {
		t.Fatalf("%+v", err)
	}

	expected := etcdMembersResponse{
		Members: []etcdMember{
			{Name: "two", ID: 5782640540428238474, ClientURLs: []string{"http://localhost:3379"}},
			{Name: "default", ID: 10276657743932975437, ClientURLs: []string{"http://localhost:2379"}},
		},
	}

	if !reflect.DeepEqual(expected, r) {
		t.Errorf("expected %#v, got %#v", expected, r)
	}
}
