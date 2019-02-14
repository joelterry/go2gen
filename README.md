# go2gen

go2gen is a prototype implementation of the [proposed](https://go.googlesource.com/proposal/+/master/design/go2draft-error-handling.md) Go 2 check/handle keywords. It transpiles invalid .go2 files with check/handle syntax into valid .go file counterparts.

go2gen is just a playful experiment, and is not meant to be used seriously. I personally enjoyed being able to get a feeling for what using check/handle would be like; however, the extra build step and lack of immediate error highlighting disqualify it from actual use.

PS: In VS Code, check/handle don't seem to interfere with syntax highlighting; to get it, add     
```
"files.associations": {
        "*.go2": "go"
},
```
to your user settings.

## Usage

```
go get github.com/joelterry/go2gen
```

To transpile .go2 files in the current directory:
```
$ go2gen
```

To transpile .go2 files in a specific directory:
```
$ go2gen DIR_PATH
```

I progressively type-check the generated package to create variable names that include their types, so the program can't be run on a per-file basis.

## Comments

Comments are currently not preserved. The standard Go AST doesn't handle modification well RE comments (https://github.com/golang/go/issues/20744). While https://github.com/dave/dst was initially a great solution, I later decided to progressively type-check the package, which required the standard AST. 