"""Special exit codes for shell control flow and internal signaling"""

# Control flow exit codes (used by break/continue)
EXIT_CODE_CONTINUE = -995  # Signal continue statement in loop
EXIT_CODE_BREAK = -996     # Signal break statement in loop

# Collection signal codes (used by REPL to collect multi-line constructs)
EXIT_CODE_FOR_LOOP_NEEDED = -997      # Signal that for loop needs to be collected
EXIT_CODE_IF_STATEMENT_NEEDED = -998  # Signal that if statement needs to be collected
EXIT_CODE_HEREDOC_NEEDED = -999       # Signal that heredoc data needs to be read
EXIT_CODE_FUNCTION_DEF_NEEDED = -1000 # Signal that function definition needs to be collected
EXIT_CODE_RETURN = -1001              # Signal return statement in function
