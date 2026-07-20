# function-python

A minimal python312 function handler.

## Deploy

```
faas deploy --template function-python
```

The CLI forces `--runtime python312 --handler handler.handler` so the
function runner wires the invocation to your exported `handler`.

## Invoke

```
faas open   # browser test page
```
