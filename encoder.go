// Copyright 2018 Alexander Poltoratskiy. All rights reserved.
// Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

package fast

import (
	"bytes"
	"io"
	"sync"
)

// A Encoder encodes and writes data to io.Writer.
type Encoder struct {
	repo map[uint]*Template
	storage storage

	tid uint // template id

	pMaps []*pMap
	pMapIndex int // index for current presence map

	writers []*writer
	writerIndex int // index for current writer

	msg *message

	target io.Writer

	logger *writerLog
	mu sync.Mutex
}

// NewEncoder returns a new encoder that writes FAST-encoded message to writer.
func NewEncoder(writer io.Writer, tmps ...*Template) *Encoder {
	encoder := &Encoder{
		repo: make(map[uint]*Template),
		storage: make(map[string]interface{}),
		target: writer,
	}
	for _, t := range tmps {
		encoder.repo[t.ID] = t
	}
	return encoder
}

// SetLog sets writer for logging
func (e *Encoder) SetLog(writer io.Writer) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if writer != nil {
		e.logger = wrapWriterLog(writer)
		return
	}

	if e.logger != nil {
		e.logger = nil
	}
}

// Encode encodes msg struct to writer
func (e *Encoder) Encode(msg interface{}) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.pMaps = []*pMap{}
	e.pMapIndex = 0
	e.writers = []*writer{}
	e.writerIndex = 0

	e.log("// ----- new message start ----- //\n")
	e.msg = newMsg(msg)
	e.tid = e.msg.GetTID()

	tpl, ok := e.repo[e.tid]
	if !ok {
		return ErrD9
	}

	e.acceptPMap()
	e.addWriter()
	e.log("template = ", e.tid)
	e.log("\n  encoding -> ")
	e.acceptTemplateID(uint32(e.tid))

	e.encodeSegment(tpl.Instructions)
	e.log("\n")
	e.commit()
	e.log("\n")

	return nil
}

func (e *Encoder) writePMap() {
	if e.pMaps[e.pMapIndex].bitmap != 0 {
		e.writers[e.writerIndex].WritePMap(e.pMaps[e.pMapIndex])
	}
}

func (e *Encoder) acceptPMap() {
	m := &pMap{mask: defaultMask}

	if len(e.pMaps) > 0 {
		e.pMapIndex++
	}

	e.pMaps = append(e.pMaps, m)
}

func (e *Encoder) restorePMap() {
	e.pMaps = e.pMaps[:e.pMapIndex]
	e.pMapIndex--
}

func (e *Encoder) addWriter() {
	if e.logger != nil {
		e.writers = append(e.writers, newWriter(e.logger, wrapWriterLog(e.logger.log)))
	} else {
		e.writers = append(e.writers, newWriter(&bytes.Buffer{}, &bytes.Buffer{}))
	}
	e.writerIndex = len(e.writers) -1
}

func (e *Encoder) commit() error {
	tmp := &bytes.Buffer{}
	for _, writer := range e.writers {
		writer.WriteTo(tmp)
	}
	tmp.WriteTo(e.target)
	return nil
}

func (e *Encoder) acceptTemplateID(id uint32) {
	e.pMaps[e.pMapIndex].SetNextBit(true)
	e.writers[e.writerIndex].WriteUint32(false, id)
}

func (e *Encoder) encodeSegment(instructions []*Instruction) {
	for _, instruction := range instructions {
		if instruction.Type == TypeSequence {
			e.encodeSequence(instruction)
		} else {
			field := &field{
				id: instruction.ID,
				name: instruction.Name,
				templateID: e.tid,
			}

			e.msg.Get(field)
			e.log("\n", instruction.Name, " = ", field.value, "\n")
			e.log("  encoding -> ")
			instruction.inject(e.writers[e.writerIndex], e.storage, e.pMaps[e.pMapIndex], field.value)
		}
	}
	e.log("\npmap = ", e.pMaps[e.pMapIndex], "\n")
	e.log("  encoding -> ")

	e.writePMap()
}

func (e *Encoder) encodeSequence(instruction *Instruction) {
	parent := &field{
		id: instruction.ID,
		name: instruction.Name,
		templateID: e.tid,
	}

	e.msg.GetLen(parent)
	length := parent.value.(int)

	e.log("\nsequence start: ")
	e.log("\n  length = ", length, "\n")
	e.log("    encoding -> ")
	instruction.Instructions[0].inject(e.writers[e.writerIndex], e.storage, e.pMaps[e.pMapIndex], uint32(length))

	current := e.writerIndex // remember current writer index
	for i:=0; i<length; i++ {
		parent.num = i
		e.log("\n  sequence elem[", i, "] start: ")

		e.acceptPMap()
		e.addWriter()

		e.msg.Lock(parent)
		e.encodeSegment(instruction.Instructions[1:])
		e.msg.Unlock()

		e.restorePMap()
	}
	e.writerIndex = current // restore index
}

func (e *Encoder) log(param ...interface{}) {
	if e.logger == nil {
		return
	}

	e.logger.Log(param...)
}
