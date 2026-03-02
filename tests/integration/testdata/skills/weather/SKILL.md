---
name: weather
description: Weather lookup via wttr.in
version: 1.0.0
triggers:
  - weather
  - forecast
  - temperature
requires:
  bins: ["curl"]
---
# Weather Skill

Check weather using wttr.in:

```bash
curl -s "wttr.in/${LOCATION}?format=3"
```

For detailed forecast:
```bash
curl -s "wttr.in/${LOCATION}"
```
