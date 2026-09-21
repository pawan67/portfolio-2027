---
title: HELM
summary: A self-hosted console for the hardware in one house — an ESP32 on the pull-up bar, MQTT in the middle, no cloud account anywhere.
year: 2026
stack: ["Next.js", "Postgres", "MQTT", "ESP32", "TypeScript"]
role: "Design, firmware and engineering"
repo: "https://github.com/pawan67/helm"
order: 2
---

Consumer smart-home hardware works until the vendor decides it should not. HELM
is the other arrangement: one operator, one server, devices that report to a
broker inside the house and take orders from the same place.

Today it runs the bar node — an ESP32 with a time-of-flight sensor that counts
pull-up reps and times dead hangs, a temperature and humidity sensor, and a
buzzer.

```
ESP32 bar node   ──MQTT──▶  Mosquitto  ──sub──▶  Next.js server  ──SSE──▶  Browser
 VL53L0X (reps)             (broker)             (persists +               (live UI)
 DHT11 (climate)                                  relays)  ──▶  Postgres
 buzzer
```

## Testing firmware from a laptop

The detection state machine runs on the device, because a rep counter that needs
the network in order to count is not one. But the same algorithm also exists in
TypeScript, in `web/src/lib/detection.ts`, as the reference implementation the
unit tests exercise. Keeping the two mirrored is a real cost, paid deliberately:
it means the logic burned onto the ESP32 has a test suite, which it would not
have if it only existed as an `.ino` file.

Nothing about developing the console requires the hardware to be plugged in
either. A mock device publishes to the broker on demand — a ten-rep set, a
thirty-second hang, a month of backfilled climate readings — so the interface
can be built against traffic shaped like the real thing.

## Not reaching for the USB cable

Two things here exist because walking to the bar with a laptop is the failure
mode worth designing out.

Detection thresholds tune live. The settings screen shows the sensor's current
reading; you hang, you pull to the top, you set the rep line just above what you
saw, and the new value pushes to the device over MQTT. No reflashing, no
recompiling, no cable.

Firmware updates go the same way. A compiled `.bin` uploaded in the console is
pushed to the node, which verifies it, flashes itself and reboots. The device
panel reports signal, uptime, free memory and IP, so there is something to look
at when it does not come back.
