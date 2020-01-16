## Data Race Detection Using Static Analysis

For every ssa instruction, the tool determines:
- whether or not it is a write or read operation; A pair of racy instructions should include at LEAST one write operation. 
- whether or not it is an atomic operation; A pair of racy instructions should include at MOST one atomic operation
- whether or not more than one operation are trying to access the same memory location; A pair of racy instructions will try to access the same memory location
- identify the goroutine which the instruction is executed under; A pair of racy instructions should operate in different goroutines
- analyze the scheduling of the instruction; A pair of racy instructions should be able to be executed in parallel, as a happens-before relationship cannot be established. 

With these rules in mind, we first attempt to generate conflicting pairs of instructions, taking into account pointer analysis for when two variables have the same address. After that, we try to establish Program Order using call graphs. As we traverse through each node of the call graph, all goroutines are assigned an ID number, with the mother goroutine always having a smaller ID than the child goroutine. 

TODO:

Channel Operations: used to establish synchronization order between conflicting access pairs. 

Synchronization Order: Sync blocks, seperated by go instructions along with channel operations (send, receive, close) and mutex operations (lock and unlock) and atomic instructions. 

