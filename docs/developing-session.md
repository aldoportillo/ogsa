## Issues

1. It seems as if when `ctrl+c` også forgets everything since everything excists in memory
2. The model has no map of the project, so it always reads by glob so when the user query is more 
   open ended. It does more tool calls leading to more tokens being spent and slower response.

## Other Tools

Claude has this folder called `.claude` on project root after some R&D it seems like it does 3 things
1. CLAUDE.md - short project description, project structure, style guide and commands
2. Facts about the project
3. Session history to restore a previous session

## Could a Graph work?

[Aider](https://aider.chat/docs/repomap.html) developed the concept of a repo map. This scans the code
to pull out important functions/types per file. Builds a graph of relationships. Ranks by importance and
the top of that ranking is stored into a small token budget. 

This could make it easy for the model to know that the Client.Detect in client.go is without opening. 

## Attack Plan

- Develop better session storage using a similar structure to claude folder
- Implement a graph
