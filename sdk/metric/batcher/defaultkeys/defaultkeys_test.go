// Copyright The OpenTelemetry Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package defaultkeys_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/otel/api/core"
	export "go.opentelemetry.io/otel/sdk/export/metric"
	"go.opentelemetry.io/otel/sdk/metric/batcher/defaultkeys"
	"go.opentelemetry.io/otel/sdk/metric/batcher/test"
)

func TestGroupingStateless(t *testing.T) {
	ctx := context.Background()
	b := defaultkeys.New(test.NewAggregationSelector(), test.GroupEncoder, false)

	_ = b.Process(ctx, test.NewLastValueRecord(&test.LastValueADesc, test.Labels1, 10))
	_ = b.Process(ctx, test.NewLastValueRecord(&test.LastValueADesc, test.Labels2, 20))
	_ = b.Process(ctx, test.NewLastValueRecord(&test.LastValueADesc, test.Labels3, 30))

	_ = b.Process(ctx, test.NewLastValueRecord(&test.LastValueBDesc, test.Labels1, 10))
	_ = b.Process(ctx, test.NewLastValueRecord(&test.LastValueBDesc, test.Labels2, 20))
	_ = b.Process(ctx, test.NewLastValueRecord(&test.LastValueBDesc, test.Labels3, 30))

	_ = b.Process(ctx, test.NewCounterRecord(&test.CounterADesc, test.Labels1, 10))
	_ = b.Process(ctx, test.NewCounterRecord(&test.CounterADesc, test.Labels2, 20))
	_ = b.Process(ctx, test.NewCounterRecord(&test.CounterADesc, test.Labels3, 40))

	_ = b.Process(ctx, test.NewCounterRecord(&test.CounterBDesc, test.Labels1, 10))
	_ = b.Process(ctx, test.NewCounterRecord(&test.CounterBDesc, test.Labels2, 20))
	_ = b.Process(ctx, test.NewCounterRecord(&test.CounterBDesc, test.Labels3, 40))

	checkpointSet := b.CheckpointSet()
	b.FinishedCollection()

	records := test.NewOutput(test.GroupEncoder)
	err := checkpointSet.ForEach(records.AddTo)
	require.NoError(t, err)

	// Repeat for {counter,lastvalue}.{1,2}.
	// Output lastvalue should have only the "G=H" and "G=" keys.
	// Output counter should have only the "C=D" and "C=" keys.
	require.EqualValues(t, map[string]float64{
		"sum.a/C=D":       30, // labels1 + labels2
		"sum.a/C=":        40, // labels3
		"sum.b/C=D":       30, // labels1 + labels2
		"sum.b/C=":        40, // labels3
		"lastvalue.a/G=H": 10, // labels1
		"lastvalue.a/G=":  30, // labels3 = last value
		"lastvalue.b/G=H": 10, // labels1
		"lastvalue.b/G=":  30, // labels3 = last value
	}, records.Map)

	// Verify that state is reset by FinishedCollection()
	checkpointSet = b.CheckpointSet()
	b.FinishedCollection()
	_ = checkpointSet.ForEach(func(rec export.Record) error {
		t.Fatal("Unexpected call")
		return nil
	})
}

func TestGroupingStateful(t *testing.T) {
	ctx := context.Background()
	b := defaultkeys.New(test.NewAggregationSelector(), test.GroupEncoder, true)

	counterA := test.NewCounterRecord(&test.CounterADesc, test.Labels1, 10)
	caggA := counterA.Aggregator()
	_ = b.Process(ctx, counterA)

	counterB := test.NewCounterRecord(&test.CounterBDesc, test.Labels1, 10)
	caggB := counterB.Aggregator()
	_ = b.Process(ctx, counterB)

	checkpointSet := b.CheckpointSet()
	b.FinishedCollection()

	records1 := test.NewOutput(test.GroupEncoder)
	err := checkpointSet.ForEach(records1.AddTo)
	require.NoError(t, err)

	require.EqualValues(t, map[string]float64{
		"sum.a/C=D": 10, // labels1
		"sum.b/C=D": 10, // labels1
	}, records1.Map)

	// Test that state was NOT reset
	checkpointSet = b.CheckpointSet()
	b.FinishedCollection()

	records2 := test.NewOutput(test.GroupEncoder)
	err = checkpointSet.ForEach(records2.AddTo)
	require.NoError(t, err)

	require.EqualValues(t, records1.Map, records2.Map)

	// Update and re-checkpoint the original record.
	_ = caggA.Update(ctx, core.NewInt64Number(20), &test.CounterADesc)
	_ = caggB.Update(ctx, core.NewInt64Number(20), &test.CounterBDesc)
	caggA.Checkpoint(ctx, &test.CounterADesc)
	caggB.Checkpoint(ctx, &test.CounterBDesc)

	// As yet cagg has not been passed to Batcher.Process.  Should
	// not see an update.
	checkpointSet = b.CheckpointSet()
	b.FinishedCollection()

	records3 := test.NewOutput(test.GroupEncoder)
	err = checkpointSet.ForEach(records3.AddTo)
	require.NoError(t, err)

	require.EqualValues(t, records1.Map, records3.Map)

	// Now process the second update
	_ = b.Process(ctx, export.NewRecord(&test.CounterADesc, test.Labels1, caggA))
	_ = b.Process(ctx, export.NewRecord(&test.CounterBDesc, test.Labels1, caggB))

	checkpointSet = b.CheckpointSet()
	b.FinishedCollection()

	records4 := test.NewOutput(test.GroupEncoder)
	err = checkpointSet.ForEach(records4.AddTo)
	require.NoError(t, err)

	require.EqualValues(t, map[string]float64{
		"sum.a/C=D": 30,
		"sum.b/C=D": 30,
	}, records4.Map)
}
