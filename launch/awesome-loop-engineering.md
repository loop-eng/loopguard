# awesome-loop-engineering

*To be created as a separate repo: github.com/loop-eng/awesome-loop-engineering*

---

# Awesome Loop Engineering [![Awesome](https://awesome.re/badge.svg)](https://awesome.re)

> A curated list of tools, libraries, and resources for designing, monitoring, and controlling AI agent loops.

Loop engineering is the practice of building reliable, observable, and cost-effective AI agent loops. As AI agents become more autonomous, the need for structured loop design — circuit breakers, observability, budget enforcement, and trace formats — becomes critical.

## Contents

- [Circuit Breakers](#circuit-breakers)
- [Observability](#observability)
- [Cost Management](#cost-management)
- [Trace Formats](#trace-formats)
- [Agent Frameworks](#agent-frameworks)
- [Research](#research)
- [Articles](#articles)

## Circuit Breakers

- [LoopGuard](https://github.com/loop-eng/loopguard) - Circuit breaker daemon for AI agent loops. Monitors Claude Code, Codex, and Gemini CLI sessions. Detects spin loops, enforces budgets, pauses via SIGSTOP.

## Observability

- [LoopCtl](https://github.com/loop-eng/loopctl) - TUI dashboard for real-time AI agent session monitoring. *(coming soon)*

## Cost Management

- [tokscale](https://github.com/AidenYang-dev/tokscale) - Real-time token usage monitor for AI API calls.
- [ccusage](https://github.com/ryoppippi/ccusage) - Claude Code usage analytics and cost tracking.

## Trace Formats

- [LTF](https://github.com/loop-eng/ltf) - Loop Trace Format — standardized JSONL format for recording AI agent loop events. *(coming soon)*

## Agent Frameworks

- [Claude Code](https://github.com/anthropics/claude-code) - Anthropic's CLI coding agent.
- [Codex](https://github.com/openai/codex) - OpenAI's coding agent.
- [LangChain](https://github.com/langchain-ai/langchain) - Framework for building LLM-powered applications.
- [CrewAI](https://github.com/crewAIInc/crewAI) - Framework for multi-agent AI systems.

## Research

- [The Loop Problem](https://arxiv.org/abs/2401.xxxxx) - *(placeholder — search for relevant papers on AI agent loop detection)*

## Articles

- "The $437 npm test Loop: Why AI Agents Need Circuit Breakers" *(dev.to — publish alongside launch)*
- [Claude Code budget overshoot discussion](https://github.com/anthropics/claude-code/issues/4277)

## Contributing

Contributions welcome! Read the [contribution guidelines](contributing.md) first.
