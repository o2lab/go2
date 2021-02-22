# Go Tools -> Go Pointer Analysis 

Git clone from https://github.com/golang/tools

Start from commit 146a0deefdd11b942db7520f68c117335329271a

For any panic, please submit an issue with copy/paste crash stack. Thanks.

## How to Use?
Go to https://github.tamu.edu/April1989/go_pointeranlaysis/blob/master/main/main.go, check how to use the current callgraph and queries. 

## Origin-sensitive
Stable version: checkout the newest commit 

### What is Origin? 
We treat a go routine instruction as an origin entry point, and all variables/function calls inside this go rountine share the same context as their belonging go routine.

## kCFA
Stable version: checkout the newest commit 

### Main Changes
- Create k-callsite-sensitive contexts for static/invoke calls
- Generate constraints/cgnode online for invoke calls and targets when it is necessary
- Currently, skip the creation of reflection and dynamic calls due to the huge number

