"""
AST (Abstract Syntax Tree) nodes for shell control flow structures.

This module defines the node types used to represent parsed shell constructs
in a structured, type-safe manner.
"""

from dataclasses import dataclass, field
from typing import List, Optional, Union


@dataclass
class Statement:
    """Base class for all statement nodes"""
    pass


@dataclass
class CommandStatement(Statement):
    """
    A simple command execution.

    Examples:
        echo hello
        ls -la
        test -f file.txt
    """
    command: str  # Raw command string (will be parsed by shell.execute)


@dataclass
class ForStatement(Statement):
    """
    for var in items; do body; done

    Examples:
        for i in 1 2 3; do echo $i; done
        for f in *.txt; do cat $f; done
    """
    variable: str                          # Loop variable name
    items_raw: str                         # Raw items string (before expansion)
    body: List[Statement] = field(default_factory=list)


@dataclass
class WhileStatement(Statement):
    """
    while condition; do body; done

    Examples:
        while true; do echo loop; done
        while test $i -lt 10; do echo $i; i=$((i+1)); done
    """
    condition: str                         # Condition command string
    body: List[Statement] = field(default_factory=list)


@dataclass
class UntilStatement(Statement):
    """
    until condition; do body; done

    Opposite of while - executes until condition becomes true (exit code 0)
    """
    condition: str
    body: List[Statement] = field(default_factory=list)


@dataclass
class IfBranch:
    """A single if/elif branch with condition and body"""
    condition: str                         # Condition command string
    body: List[Statement] = field(default_factory=list)


@dataclass
class IfStatement(Statement):
    """
    if condition; then body; [elif condition; then body;]* [else body;] fi

    Examples:
        if test $x -eq 1; then echo one; fi
        if test -f $f; then cat $f; else echo missing; fi
    """
    branches: List[IfBranch] = field(default_factory=list)  # if + elif branches
    else_body: Optional[List[Statement]] = None             # else block


@dataclass
class FunctionDefinition(Statement):
    """
    function_name() { body; }

    Examples:
        hello() { echo "Hello $1"; }
        function greet { echo "Hi"; }
        function add(a, b) { echo $1 $2; }     # bash extension: declared params

    Note: `params` records declared parameter names from the
    `function NAME(a, b)` form. Call-time argument binding still uses
    `$1`, `$2`, ... — declared names are recorded for introspection only.
    """
    name: str
    params: List[str] = field(default_factory=list)
    body: List[Statement] = field(default_factory=list)


# Type alias for any statement
AnyStatement = Union[
    CommandStatement,
    ForStatement,
    WhileStatement,
    UntilStatement,
    IfStatement,
    FunctionDefinition
]
