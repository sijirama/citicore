# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Stack

Go API (inferred from the Ferno core reference).

## Users

- Public users asking civic questions through the Citizen client and mini apps.
- Allowlisted Citizen administrators managing documents and content.

## Product Purpose

Provide API services for Citizen.ng's document-grounded chat, administrator content workspace, and separately maintained mini apps.

## Capabilities and Constraints

- Admin authentication uses Google OAuth and a server-side email allowlist.
- The chat agent should follow Ferno's bounded runtime and tool-executor shape, with Citizen-specific prompts and document retrieval.
- No open web search or news tool is allowed in the chat agent.
- Database, model provider, embedding provider, file storage, document formats, and indexing policy remain open.

## Evidence on Hand

- Agent runtime reference: `/home/sijirama/Desktop/me/coder/ferno/core`.
- Admin allowlist reference: `/home/sijirama/Desktop/me/coder/work/ayo/core`.
