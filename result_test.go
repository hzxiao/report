package main

import (
	"fmt"
	"github.com/Centny/gwf/util"
	"testing"
)

func TestHandleReport(t *testing.T) {
	var err error

	err = initDB()
	if err != nil {
		t.Error(err)
		return
	}

	dbFile = "test.db"

	err = handleReport(util.Map{
		"http": util.Map{
			"used": []util.Map{
				{
					"name": "abc",
				},
			},
		},
	})
	if err != nil {
		t.Error(err)
		return
	}

	rep, err := report()
	if err != nil {
		t.Error(err)
		return
	}

	fmt.Println(util.S2Json(rep))
}
