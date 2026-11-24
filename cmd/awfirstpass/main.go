// awfirstpass is a strict stdin/stdout, read-only request-evidence adapter. No database, probe or writer.
package main

import (
	"autoweight/firstpass"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

func main() {
	var in firstpass.Input
	d := json.NewDecoder(os.Stdin)
	d.DisallowUnknownFields()
	if err := d.Decode(&in); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if d.Decode(new(any)) != io.EOF {
		fmt.Fprintln(os.Stderr, "trailing input")
		os.Exit(1)
	}
	out, err := firstpass.Evaluate(in)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err = json.NewEncoder(os.Stdout).Encode(out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
