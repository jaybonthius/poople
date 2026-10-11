# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Stack

Go, Chi, Templ, Datastar with Rocket web components, SQLite; server-rendered CQRS with full-view morphs, following the owner's getajob app.

## Users

The owner and their friends, playing a daily four-letter word ladder, mostly on phones and also on desktop, and comparing results with each other. Strangers who find the site can play anonymously or sign up.

## Product Purpose

A private fork of Poople (pooplegame.com): turn the day's start word into POOP one letter at a time. It keeps Poople's daily rhythm and fixes what frustrates the group: the small word list. Success is a quick, satisfying daily puzzle the group actually plays and compares.

## Positioning

Poople's game with the full Collins Scrabble Words 2024 four-letter list, friend accounts with profile pictures, leaderboards and an unlimited mode, while the server keeps the calendar and solutions hidden.

## Capabilities and Constraints

- Anonymous play needs nothing; accounts are optional, password-protected, open to anyone and carry a profile picture. The first account, created from a setup link printed by the server, is the admin.
- Daily puzzle switches at 08:00 UTC. Par is the shortest ladder in our word list; puzzles freeze their word list, start word and par when first played.
- Finishing shows steps against par, a shortest solution, share, the countdown and that day's leaderboard.
- Personal statistics, shareable results and an unlimited mode.
- Terms: start word, target (POOP), guess or step, ladder, par.

## Brand Commitments

Keep the name "Poople" and the poop emoji mascot. Look and layout are ours to design.

## Evidence on Hand

Reference captures of the original game's play, finish and statistics screens: `docs/design/reference/`. No testimonials, user counts or claims exist; do not invent them.

## Product Principles

- The puzzle comes first: the board and keyboard are usable immediately on any screen.
- Friendly competition: results and leaderboards are visible after you finish, never spoilers before.
- Fast and honest: every guess is confirmed by the server before the board shows it.
- Playful but tidy: poop humour lives in small details, not clutter.
