/*************************************************************************
 * Copyright 2018 Gravwell, Inc. All rights reserved.
 * Contact: <legal@gravwell.io>
 *
 * This software may be modified and distributed under the terms of the
 * BSD 2-clause license. See the LICENSE file for details.
 **************************************************************************/

package processors

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"

	"github.com/gravwell/ingest/v3/config"
	"github.com/gravwell/ingest/v3/entry"
)

const (
	RegexExtractProcessor = `regexextract`
)

var (
	st  = []byte(`${`)
	end = []byte(`}`)
)

type RegexExtractConfig struct {
	Passthrough_Misses bool
	Regex              string
	Template           string
}

func RegexExtractLoadConfig(vc *config.VariableConfig) (c RegexExtractConfig, err error) {
	err = vc.MapTo(&c)
	return
}

type RegexExtractor struct {
	nocloser
	RegexExtractConfig
	tmp *formatter
	rx  *regexp.Regexp
	cnt int
}

func NewRegexExtractor(cfg RegexExtractConfig) (*RegexExtractor, error) {
	rx, tmp, err := cfg.validate()
	if err != nil {
		return nil, err
	}

	return &RegexExtractor{
		RegexExtractConfig: cfg,
		tmp:                tmp,
		rx:                 rx,
		cnt:                len(rx.SubexpNames()),
	}, nil
}

func (re *RegexExtractor) Config(v interface{}) (err error) {
	if v == nil {
		err = ErrNilConfig
	} else if cfg, ok := v.(RegexExtractConfig); ok {
		if re.rx, re.tmp, err = cfg.validate(); err == nil {
			re.RegexExtractConfig = cfg
		}
	} else {
		err = fmt.Errorf("Invalid configuration, unknown type type %T", v)
	}
	return
}

func (re *RegexExtractor) Process(ent *entry.Entry) (rset []*entry.Entry, err error) {
	if ent == nil {
		return
	}
	if mtchs := re.rx.FindSubmatch(ent.Data); len(mtchs) == re.cnt {
		ent.Data = re.tmp.render(mtchs)
		rset = []*entry.Entry{ent}
	} else if re.Passthrough_Misses {
		rset = []*entry.Entry{ent}
	} //else is a DROP
	return
}

func (c RegexExtractConfig) validate() (rx *regexp.Regexp, tmp *formatter, err error) {
	if c.Regex == `` {
		err = errors.New("Missing regular expression")
		return
	} else if c.Template == `` {
		err = errors.New("Missing template")
		return
	} else if tmp, err = newFormatter(c.Template); err != nil {
		return
	} else if rx, err = regexp.Compile(c.Regex); err != nil {
		return
	}
	names := rx.SubexpNames()
	if len(names) == 0 {
		err = ErrMissingExtractNames
		return
	}
	err = tmp.setReplaceNames(names)
	return
}

type replaceNode interface {
	Bytes([][]byte) []byte
}

type formatter struct {
	nodes []replaceNode
	bb    *bytes.Buffer
}

func newFormatter(s string) (f *formatter, err error) {
	var nodes []replaceNode
	v := []byte(s)
	for len(v) > 0 {
		var n replaceNode
		if n, v, err = consumeNode(v); err != nil {
			return
		}
		nodes = append(nodes, n)

	}
	f = &formatter{
		nodes: nodes,
		bb:    bytes.NewBuffer(nil),
	}
	return
}

func (f *formatter) setReplaceNames(names []string) (err error) {
	for i := range f.nodes {
		if lu, ok := f.nodes[i].(*lookupNode); ok {
			if lu.idx = getStringIndex(lu.name, names); lu.idx == -1 {
				err = fmt.Errorf("Replacement name %s not found in regular expression list", lu.name)
				break
			}
		}
	}
	return
}

func (f *formatter) render(vals [][]byte) (data []byte) {
	f.bb.Reset()
	for i := range f.nodes {
		f.bb.Write(f.nodes[i].Bytes(vals))
	}
	data = append([]byte{}, f.bb.Bytes()...)
	return
}

func getStringIndex(needle string, haystack []string) int {
	for i, n := range haystack {
		if needle == n {
			return i
		}
	}
	return -1
}

type constNode struct {
	val []byte
}

func (c constNode) Bytes(lo [][]byte) []byte {
	return c.val
}

type lookupNode struct {
	name string
	idx  int
}

func (l lookupNode) Bytes(lo [][]byte) []byte {
	if l.idx <= len(lo) && l.idx >= 0 {
		return lo[l.idx]
	}
	return nil
}

func consumeNode(v []byte) (n replaceNode, r []byte, err error) {
	if len(v) == 0 {
		return
	}
	stidx := bytes.Index(v, st)
	switch stidx {
	case 0: // start of lookup node
		v = v[len(st):]
		//start of replacement node, find the end
		eidx := bytes.Index(v, end)
		if eidx == -1 {
			err = errors.New("Closing curly bracket } missing missing on field")
			return
		}
		r = v[eidx+1:]
		n = &lookupNode{
			name: string(v[:eidx]),
		}
	case -1: //completely missed
		//end of string, consume as a const node
		n = &constNode{v}
	default: //eat a constant first
		n = &constNode{v[:stidx]}
		r = v[stidx:]
	}
	return
}
