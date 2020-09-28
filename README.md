# Go2 Race Detector

[![Github Actions Status]](https://github.com/dopelsunce/race-go2/actions)

## Static Analysis - Concurrency Bug Detection in Go
### Data Races

The tool makes use of __static single-assignment (SSA)__ form intermediate representation to construct goroutine-specific __stack traces__ discretely for the basis of static analysis. Throughout the process of constructing each stack trace, Andersen's inclusion-based __pointer analysis__ algorithm is used on an as-need basis[1]. Due to the flow-insensitive and context-insensitive nature of the adopted algorithm, there's potential for ambiguity in the points-to information returned by the analysis[2]. Upon completing the construction of stack traces, a __Happens-Before graph__ is built to enforce flow-sensitive analysis. Ultimately, racy instructions are reported, each along with its corresponding call stack. 

**[3]

****************************************************************************************************
_Work in Progress:_

[1] such repetitive procedures are significantly costing performance of the tool. A more efficient algorithm may be put in place to minimize frequency of conducting pointer analysis. 

[2] for the time being, in the case of multiple targets being returned by pointer analysis, the call stack is used to determine which target in the points-to set to execute. Need a much more refined method to consider calling context of functions in the code base. 

[3] need to consider how Go handles communication and synchronization more precisely. 
