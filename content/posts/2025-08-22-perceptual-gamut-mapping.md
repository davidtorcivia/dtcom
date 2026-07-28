---
title: Perceptual Gamut Mapping in DaVinci Resolve & ACES Workflows
date: 2025-08-22
description: Getting filmic color out of wide-gamut pipelines without crushing the colors that matter.
tags: [color, davinci, aces]
draft: false
---

Wide-gamut pipelines have a dirty secret: most of the colors you're protecting don't exist on the display you're mastering for. ACES and Rec.2020 working spaces buy you headroom, but they don't buy you a free lunch on the way back to Rec.709 or P3. If you let a straight gamut compression do the work, you get a film that looks correct on a scopes window and dead on a screen.

The fix is to map perceptually, not mathematically. ==A viewer doesn't care that your cyan is 8% more saturated than the display can render—they care that skin tones hold up and that the highlights don't posterize.== That means prioritizing luminance and hue over raw chroma when something has to give, and accepting that some out-of-gamut colors should clip cleanly rather than be smeared into something muddy.

> Gamut mapping is a compression problem, not a clipping problem. Compress where the eye forgives, clip where the eye doesn't notice.

In Resolve, this means doing the heavy lifting in the Color page with a DCTL or a custom node tree before the ODT, not relying on the ODT alone to catch it. The ACES Output Transforms are designed to be safe across a huge range of material, which is exactly why they're conservative. If you've shot log and graded with intent, push the mapping decision back into your grade, where you can see it on a calibrated display, and let the ODT do only what it's good at: tone mapping the highlights down to the target transfer function.
