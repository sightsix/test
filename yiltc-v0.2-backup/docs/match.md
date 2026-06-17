# Yilt Match

`match` is Yilt's structured branch form for choosing between values.

## Syntax

```yilt
match value
  case 1
    print("one")
  case 2
    print("two")
  default
    print("other")
Notes

match evaluates one subject expression.
case arms are checked top to bottom.
default is optional.
Bodies are indented like other Yilt blocks.
The current implementation is a strict value match, not a
destructuring pattern matcher.
String comparisons use content equality, not pointer identity.
An empty match block is rejected.

Practical Use

Use match when you want readable dispatch over a small
set of stable values.
