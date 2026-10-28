## The Situation

We are a regulated financial company with 400 people and about 60 internal tools. People ask for access in chat. Whoever is admin grants it. Nobody ever removes it.

## Who Cares About This

### InfoSec
As little access as possible.

### Managers
Their people unblocked fast.

### Audit
Proof to show the regulator.

### HR
Knows who joins, moves and leaves.

## How It Works Today

- People ask in a Slack channel or message an admin directly.
- Every tool has its own admin, who adds people by hand. Some tools have a user screen, some have an API, and some need a ticket to the vendor.
- Nothing is written down: not who asked, who approved, or why.
- Admins hear about joiners, movers and leavers late, or not at all.

## Why It's a Problem

- People keep access long after they need it. Last month's audit found 14 people who had left still had accounts.
- Nobody can prove who approved sensitive access.
- The regulator wants an access review every quarter, with proof. Today we can't give one.
- Each admin decides alone, so the same request gets different answers in different tools.

---

## What We Want

We want to fix this process with software you write: a working program that runs access from now on. At a minimum it should cover:

### 1. Requests
Someone asks for access to a tool, says why, and says how long they need it.

### 2. Approvals
The right people approve, depending on the tool and how sensitive the access is.

### 3. Granting and Removing
Access is added in the tool. It is removed when it expires, when the person changes team, or when they leave.

### 4. Reviews
Every quarter, each tool's owner confirms who keeps access. The program chases owners who don't reply.

### 5. Proof
Audit can see who had access to what on any date, and who approved it.

---

## Stack

Go for the backend and React for the frontend, because that's what we use.

Any database.

It should start with one command, for example:

```bash
docker compose up
```

Any tools are fine, AI included.

## What We Don't Give You

No sample data and no list of rules. You decide what the program does and which rules it follows.

---

## Hand In Within 5 Days

Make it work end to end before you make it pretty.

### Deliverables

1. The code with a short README, and a 5-minute recording of it working.
2. One page: how it runs each week, the rules it follows, and what you assumed.
3. What you'd do in your first 30 days to move us from today to this.