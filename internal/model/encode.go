package model

import (
	"bytes"
	"encoding/gob"

	"raincast/internal/motion"
)

// preparedWire is Prepared with exported fields and no nil pointers, which
// gob cannot encode inside slices.
type preparedWire struct {
	Fields   []motion.Field
	Trends   []motion.Trend
	HasTrend []bool
	Accels   []motion.Field
	HasAccel []bool
	Weights  []float64
	Used     int
	Display  motion.Field
	Storms   []StormInfo
}

// MarshalBinary encodes p, e.g. to share it through a cache.
func (p *Prepared) MarshalBinary() ([]byte, error) {
	w := preparedWire{Weights: p.weights, Used: p.Used, Storms: p.Storms}
	for i, f := range p.fields {
		w.Fields = append(w.Fields, *f)
		t := p.trends[i]
		w.HasTrend = append(w.HasTrend, t != nil)
		if t == nil {
			t = &motion.Trend{}
		}
		w.Trends = append(w.Trends, *t)
		a := p.accels[i]
		w.HasAccel = append(w.HasAccel, a != nil)
		if a == nil {
			a = &motion.Field{}
		}
		w.Accels = append(w.Accels, *a)
	}
	if p.Display != nil {
		w.Display = *p.Display
	}
	var buf bytes.Buffer
	err := gob.NewEncoder(&buf).Encode(w)
	return buf.Bytes(), err
}

// UnmarshalBinary decodes what MarshalBinary encoded.
func (p *Prepared) UnmarshalBinary(data []byte) error {
	var w preparedWire
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&w); err != nil {
		return err
	}
	*p = Prepared{weights: w.Weights, Used: w.Used, Storms: w.Storms}
	for i := range w.Fields {
		p.fields = append(p.fields, &w.Fields[i])
		var t *motion.Trend
		if w.HasTrend[i] {
			t = &w.Trends[i]
		}
		p.trends = append(p.trends, t)
		// Entries cached before acceleration existed have no HasAccel.
		var a *motion.Field
		if i < len(w.HasAccel) && w.HasAccel[i] {
			a = &w.Accels[i]
		}
		p.accels = append(p.accels, a)
	}
	p.Display = &w.Display
	return nil
}
