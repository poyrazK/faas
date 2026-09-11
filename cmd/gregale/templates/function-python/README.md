# function-python

A minimal python312 function handler.

## Deploy

```
gregale deploy --template function-python --name <slug>
```

The CLI forces `--runtime python312 --handler handler.handler` so the
function runner wires the invocation to your exported `handler`.

## Invoke

```
gregale open <slug>   # browser test page
```
