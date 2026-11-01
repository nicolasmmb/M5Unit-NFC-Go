//go:build esp32

// Example: detect NFC-A cards with the M5Stack Unit NFC (ST25R3916).
package main

import (
	"context"
	"fmt"
	"time"

	"machine"

	nfc "github.com/nicolasmmb/M5Unit-NFC-Go"
)

func main() {
	time.Sleep(time.Second)

	i2c := machine.I2C1
	if err := i2c.Configure(machine.I2CConfig{
		SDA:       machine.GPIO32,
		SCL:       machine.GPIO33,
		Frequency: 400 * machine.KHz,
	}); err != nil {
		println("nfc: i2c:", err.Error())
		return
	}

	d := nfc.New(i2c)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	err := d.Begin(ctx)
	cancel()
	if err != nil {
		println("nfc: begin:", err.Error())
		return
	}

	for {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		c, ok, err := d.Detect(ctx)
		cancel()
		if err != nil {
			println("nfc:", err.Error())
		} else if ok {
			println("nfc:", fmt.Sprintf("% X", c.UID[:c.UIDLen]),
				nfc.TypeName(c.TypeID), "ATQA", c.ATQA, "SAK", c.SAK,
				"mem", c.UserMemory)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
