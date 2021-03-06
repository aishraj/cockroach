// Copyright 2015 The Cockroach Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
// implied. See the License for the specific language governing
// permissions and limitations under the License.
//
// Author: Marc Berhault (marc@cockroachlabs.com)

package sql_test

import (
	"bytes"
	"testing"
	"time"

	"golang.org/x/net/context"

	"github.com/cockroachdb/cockroach/internal/client"
	"github.com/cockroachdb/cockroach/keys"
	"github.com/cockroachdb/cockroach/roachpb"
	"github.com/cockroachdb/cockroach/server"
	"github.com/cockroachdb/cockroach/testutils/serverutils"
	"github.com/cockroachdb/cockroach/util"
	"github.com/cockroachdb/cockroach/util/leaktest"
	"github.com/pkg/errors"
)

// getRangeKeys returns the end keys of all ranges.
func getRangeKeys(db *client.DB) ([]roachpb.Key, error) {
	rows, err := db.Scan(context.TODO(), keys.Meta2Prefix, keys.MetaMax, 0)
	if err != nil {
		return nil, err
	}
	ret := make([]roachpb.Key, len(rows), len(rows))
	for i := 0; i < len(rows); i++ {
		ret[i] = bytes.TrimPrefix(rows[i].Key, keys.Meta2Prefix)
	}
	return ret, nil
}

func getNumRanges(db *client.DB) (int, error) {
	rows, err := getRangeKeys(db)
	if err != nil {
		return 0, err
	}
	return len(rows), nil
}

func rangesMatchSplits(ranges []roachpb.Key, splits []roachpb.RKey) bool {
	if len(ranges) != len(splits) {
		return false
	}
	for i := 0; i < len(ranges); i++ {
		if !splits[i].Equal(ranges[i]) {
			continue
		}
	}
	return true
}

// TestSplitOnTableBoundaries verifies that ranges get split
// as new tables get created.
func TestSplitOnTableBoundaries(t *testing.T) {
	defer leaktest.AfterTest(t)()

	params, _ := createTestServerParams()
	// We want fast scan.
	params.ScanInterval = time.Millisecond
	params.ScanMaxIdleTime = time.Millisecond
	s, sqlDB, kvDB := serverutils.StartServer(t, params)
	defer s.Stopper().Stop()

	expectedInitialRanges := server.ExpectedInitialRangeCount()

	if _, err := sqlDB.Exec(`CREATE DATABASE test`); err != nil {
		t.Fatal(err)
	}

	// We split up to the largest allocated descriptor ID, be it a table
	// or a database.
	util.SucceedsSoon(t, func() error {
		num, err := getNumRanges(kvDB)
		if err != nil {
			return err
		}
		if e := expectedInitialRanges + 1; num != e {
			return errors.Errorf("expected %d splits, found %d", e, num)
		}
		return nil
	})

	// Verify the actual splits.
	objectID := uint32(keys.MaxReservedDescID + 1)
	splits := []roachpb.RKey{keys.MakeTablePrefix(objectID), roachpb.RKeyMax}
	ranges, err := getRangeKeys(kvDB)
	if err != nil {
		t.Fatal(err)
	}
	if a, e := ranges[expectedInitialRanges-1:], splits; !rangesMatchSplits(a, e) {
		t.Fatalf("Found ranges: %v\nexpected: %v", a, e)
	}

	// Let's create a table.
	if _, err := sqlDB.Exec(`CREATE TABLE test.test (k INT PRIMARY KEY, v INT)`); err != nil {
		t.Fatal(err)
	}

	util.SucceedsSoon(t, func() error {
		num, err := getNumRanges(kvDB)
		if err != nil {
			return err
		}
		if e := expectedInitialRanges + 2; num != e {
			return errors.Errorf("expected %d splits, found %d", e, num)
		}
		return nil
	})

	// Verify the actual splits.
	splits = []roachpb.RKey{keys.MakeTablePrefix(objectID), keys.MakeTablePrefix(objectID + 1), roachpb.RKeyMax}
	ranges, err = getRangeKeys(kvDB)
	if err != nil {
		t.Fatal(err)
	}
	if a, e := ranges[expectedInitialRanges-1:], splits; !rangesMatchSplits(a, e) {
		t.Fatalf("Found ranges: %v\nexpected: %v", a, e)
	}
}
