// Copyright (c) 2017, Shreyas Khare <skhare@rapid7.com>
// See LICENSE for licensing information

package main

import (
	"encoding/csv"
	"io"
	"log"
	"net/http"
	"os"
	"text/template"
)

const path = "schemes.go"

var schemesTmpl = template.Must(template.New("schemes").Parse(`// Generated by schemesgen

package xurls

// Schemes is a sorted list of all IANA assigned schemes.
//
// Source:
//   https://www.iana.org/assignments/uri-schemes/uri-schemes-1.csv
var Schemes = []string{
{{range $scheme := .Schemes}}` + "\t`" + `{{$scheme}}` + "`" + `,
{{end}}}
`))

func schemeList() []string {
	resp, err := http.Get("https://www.iana.org/assignments/uri-schemes/uri-schemes-1.csv")
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()
	r := csv.NewReader(resp.Body)
	r.Read() // ignore headers
	schemes := make([]string, 0)
	for {
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatal(err)
		}
		schemes = append(schemes, record[0])
	}
	return schemes
}

func writeSchemes(schemes []string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return schemesTmpl.Execute(f, struct {
		Schemes []string
	}{
		Schemes: schemes,
	})
}

func main() {
	schemes := schemeList()
	log.Printf("Generating %s...", path)
	if err := writeSchemes(schemes); err != nil {
		log.Fatalf("Could not write path: %v", err)
	}
}
