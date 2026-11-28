// Copyright 2023 Juan Pablo Tosso and the OWASP Coraza contributors
// SPDX-License-Identifier: Apache-2.0

package collections

import (
	"regexp"
	"strings"

	"github.com/corazawaf/coraza/v3/collection"
	"github.com/corazawaf/coraza/v3/internal/corazarules"
	"github.com/corazawaf/coraza/v3/types"
	"github.com/corazawaf/coraza/v3/types/variables"
)

// ConcatCollection is a collection view over multiple collections.
type ConcatCollection struct {
	data     []collection.Collection
	variable variables.RuleVariable
}

var _ collection.Collection = &ConcatCollection{}

func NewConcatCollection(variable variables.RuleVariable, data ...collection.Collection) *ConcatCollection {
	return &ConcatCollection{
		data:     data,
		variable: variable,
	}
}

// FindAll returns all matches for all collections
func (c *ConcatCollection) FindAll() []types.MatchData {
	var res []types.MatchData
	for _, d := range c.data {
		res = append(res, replaceVariable(c.variable, d.FindAll())...)
	}
	return res
}

// Name returns the name for the current CollectionconcatCollection
func (c *ConcatCollection) Name() string {
	return c.variable.Name()
}

// ConcatKeyed is a collection view over multiple keyed collections.
type ConcatKeyed struct {
	data     []collection.Keyed
	variable variables.RuleVariable
	// all caches FindAll while every part reports the generations in gens.
	all  []types.MatchData
	gens []uint64
}

// generational is a collection that counts its writes.
type generational interface{ generation() uint64 }

var _ collection.Keyed = &ConcatKeyed{}

func NewConcatKeyed(variable variables.RuleVariable, data ...collection.Keyed) *ConcatKeyed {
	return &ConcatKeyed{
		data:     data,
		variable: variable,
	}
}

func (c *ConcatKeyed) Get(key string) []string {
	keyL := strings.ToLower(key)
	var res []string
	for _, c := range c.data {
		res = append(res, c.Get(keyL)...)
	}
	return res
}

// FindRegex returns a slice of MatchData for the regex
func (c *ConcatKeyed) FindRegex(key *regexp.Regexp) []types.MatchData {
	var res []types.MatchData
	for _, d := range c.data {
		res = append(res, replaceVariable(c.variable, d.FindRegex(key))...)
	}
	return res
}

// FindString returns a slice of MatchData for the string
func (c *ConcatKeyed) FindString(key string) []types.MatchData {
	var res []types.MatchData
	for _, d := range c.data {
		res = append(res, replaceVariable(c.variable, d.FindString(key))...)
	}
	return res
}

// FindAll returns all matches for all collections. When every part counts its
// writes the result is built once per generation; the matches are copies
// carrying the concatenated variable, so the parts' cached matches stay as
// they are.
func (c *ConcatKeyed) FindAll() []types.MatchData {
	if !c.cacheValid() {
		var res []types.MatchData
		for _, d := range c.data {
			res = append(res, d.FindAll()...)
		}
		res = replaceVariable(c.variable, res)
		if !c.recordGens() {
			return res
		}
		c.all = res
	}
	return cloneMatches(c.all)
}

func (c *ConcatKeyed) cacheValid() bool {
	if c.all == nil || len(c.gens) != len(c.data) {
		return false
	}
	for i, d := range c.data {
		g, ok := d.(generational)
		if !ok || g.generation() != c.gens[i] {
			return false
		}
	}
	return true
}

func (c *ConcatKeyed) recordGens() bool {
	gens := make([]uint64, len(c.data))
	for i, d := range c.data {
		g, ok := d.(generational)
		if !ok {
			c.all, c.gens = nil, nil
			return false
		}
		gens[i] = g.generation()
	}
	c.gens = gens
	return true
}

// Name returns the name for the current CollectionconcatCollection
func (c *ConcatKeyed) Name() string {
	return c.variable.Name()
}

// replaceVariable ensures a returned match references the variable of a concatenated variable,
// not original one. It copies the matches rather than changing them: a part's
// FindAll hands out matches it keeps cached. md must be a slice the caller owns.
func replaceVariable(v variables.RuleVariable, md []types.MatchData) []types.MatchData {
	buf := make([]corazarules.MatchData, len(md))
	for i, m := range md {
		buf[i] = *m.(*corazarules.MatchData)
		buf[i].Variable_ = v
		md[i] = &buf[i]
	}
	return md
}
