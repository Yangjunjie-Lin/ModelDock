# Licensing decision

Decision status: **blocked**  
Selected option: **undecided**  
Decision owner: repository owner  
Last reviewed: 2026-08-10

No license is granted by this document. There is intentionally no `LICENSE`
file. Copyright defaults apply until the owner records a decision, completes
legal review, and updates this status. The automated release workflow treats
this unresolved decision as a formal release blocker.

## Options and consequences

| Option | Distribution and contribution effect | Commercial and operational consequence | Main decision risks |
| --- | --- | --- | --- |
| Proprietary | Source use and redistribution require an explicit contract; outside contributions need a contributor agreement or assignment policy. | Preserves maximum control over hosted and on-premises commercial terms. Customers may require source escrow, audit rights, or negotiated security support. | Lower community adoption, higher contracting overhead, and ambiguity if third-party notices are incomplete. |
| Apache-2.0 | Permissive use, modification, and redistribution with notice preservation; includes an express contributor patent grant and patent retaliation. | Friendly to commercial embedding and managed-service competitors. Trademark and hosted-service differentiation must carry more of the business model. | Downstream proprietary forks need not publish changes; patent/trademark/NOTICE handling needs counsel. |
| AGPL-3.0 | Modified versions offered over a network generally require corresponding source availability to their users. | Discourages closed hosted forks and can support a paid commercial exception. Some enterprises prohibit AGPL dependencies or require extensive review. | Compatibility, combined-work scope, SaaS source-offer operations, and contributor copyright ownership require legal analysis. |
| Dual license | The same code is offered under a community license (often AGPL) and a commercial license. | Can combine open collaboration with paid proprietary embedding or managed-service rights. | Requires sufficient copyright control over every contribution, a contributor agreement/policy, consistent editions, and ongoing commercial enforcement. |

## Required decision record

Before a formal release, the owner must:

1. obtain legal advice appropriate to the intended hosted/on-premises business;
2. inventory third-party licenses and required notices for Go, npm, container,
   and build dependencies;
3. choose one option and record the exact license/version or proprietary terms;
4. define contribution copyright and any CLA/DCO requirements;
5. add the approved license/notice artifacts where applicable;
6. change `Decision status` to `approved` and `Selected option` to the exact
   decision in this document; and
7. approve the resulting pull request as CODEOWNER.

The machine-verifiable `Selected option` value must be exactly one of
`proprietary`, `apache-2.0`, `agpl-3.0-only`, or `dual-license`. Proprietary
releases also require `docs/proprietary-terms.md`; Apache/AGPL releases require
the exact `LICENSE`; dual licensing requires both `LICENSE` and
`docs/commercial-license.md`.

CI validates that this decision record remains present. Release validation
additionally requires `approved`; there is no workflow input or maintainer
override that bypasses this gate.
