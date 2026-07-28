---
title: Four Maxims for Technology
date: 2025-11-14
description: Four working principles I keep returning to when building and buying software.
tags: [philosophy, technology]
draft: false
---

I've been shipping software long enough to stop trusting cleverness and start trusting rules of thumb. These four maxims are the ones I actually repeat to myself before merging, buying, or greenlighting anything. None of them are original. All of them keep being right.

## 1. The tool is not the team

A new framework, language, or platform can solve exactly one class of problem well. It cannot resolve unclear ownership, missing specs, or a team that doesn't talk to each other. If a dysfunction shows up in your codebase, it almost certainly started in a meeting room. Reaching for a tool to fix a people problem is the most expensive way to learn this.

## 2. Boring is a feature

The boring choice is the one that's been debugged by every other person who made it. Postgres, nginx, plain HTTP, files on disk—these win not because they're elegant but because their failure modes are documented. ==When you reach for the interesting option, you are volunteering to write the documentation nobody else will ever read.== I reserve interesting technology for the part of the system that is genuinely novel, which is usually about 5% of it.

## 3. Defaults outlive decisions

People will run your software with the defaults you shipped. They will not read the README. They will not tune the knobs. Whatever you set as the out-of-the-box behavior becomes the actual behavior of your product in the wild, and the configuration surface you added "for flexibility" becomes a maintenance surface you support forever. Pick defaults like they're load-bearing, because within six months they will be.

## 4. Compute is cheaper than attention

A machine can retry, recompute, and re-index a thousand times for the cost of one engineer noticing a task is slow. Optimize for the human in the loop first. If a build takes three minutes, that's not a build-time problem—it's a focus problem, because the engineer context-switches and you lose the next hour. Spend the cycles. Guard the attention.
