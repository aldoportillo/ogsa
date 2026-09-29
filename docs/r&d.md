## Mission

This is an attempt to make open source models more accessible. 
Everyone always hears how certain models beat other 
models but people enjoy certain interfaces. 
This is an attempt at centralizing a specific interface 
and have this act more like a harness for whatever model 
without modifying the ui.

## How are other systems doing it? 

### My initial thought process

I believe that each file gets embedded for faster searching.
This allows the agent to RAG search through all files allowing
it to not need to remember all there certain project strucures.

Maybe that is over complext s something else, it could just be
grep? But how do they know what file name? Even with a fuzzy search.
If a file name is unrelated how can it find it? 

### What is happening frfr overview

Tool use loop (kinda agentic rag like i meantioned). 

User query + context -> model thinks and calls tools --> result goes back to context --> Model Decides next step


