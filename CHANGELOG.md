# Changelog

## [0.2.0](https://github.com/yorch/ccshelf/compare/v0.1.0...v0.2.0) (2026-10-07)


### Features

* **catalog:** add catalog init to bootstrap or adopt an org repo ([#13](https://github.com/yorch/ccshelf/issues/13)) ([761a8cf](https://github.com/yorch/ccshelf/commit/761a8cfda413e1016b6fcc770fdf4845afbda618))
* **install:** add verified install scripts for Linux, macOS and Windows ([#14](https://github.com/yorch/ccshelf/issues/14)) ([3ec9a84](https://github.com/yorch/ccshelf/commit/3ec9a84c8b71b6d482f5362ff065d9d0d26e23cb))
* **update:** add ccshelf update and opt-in automatic updates ([#12](https://github.com/yorch/ccshelf/issues/12)) ([7b847ff](https://github.com/yorch/ccshelf/commit/7b847ff239fb2ca5a0c76c0f2509e78227e201c4))


### Bug fixes

* **ci:** repair cross-platform checks on main ([#15](https://github.com/yorch/ccshelf/issues/15)) ([2d056b7](https://github.com/yorch/ccshelf/commit/2d056b70adf39860ec24c44334fccab5a285b52a))
* **release:** publish releases under the yorch owner ([#10](https://github.com/yorch/ccshelf/issues/10)) ([069ce4c](https://github.com/yorch/ccshelf/commit/069ce4c0b99fd9c4ecbac2091fd55d3b29cac796))
* **update:** treat lock PID diagnostics as best effort ([#16](https://github.com/yorch/ccshelf/issues/16)) ([a2e048f](https://github.com/yorch/ccshelf/commit/a2e048faf68669ad72f16f02f6a05dfa75fdd0b1))

## 0.1.0 (2026-10-07)


### Features

* add clicore, the shared CLI context ([aa970ea](https://github.com/yorch/ccshelf/commit/aa970ea07b0b705634ae7b52adc7687b0d782ea6))
* add doctor, recommend and analytics ([52cdadf](https://github.com/yorch/ccshelf/commit/52cdadf46da40bee0e9ca99362e12124253e33c8))
* add launcher commands (run, dry-run, show, ls, diff, new, edit, init, trust, account, shell-init) ([c6e320c](https://github.com/yorch/ccshelf/commit/c6e320cbe95681adcfe58ffd5a4451372dd3f89c))
* add managed-policy detection, the capability matrix and the bundle compiler ([bde448b](https://github.com/yorch/ccshelf/commit/bde448bc76603ad9ed42521088de492d49fb07a8))
* add org and catalog commands (lint, compile, catalog, search, recommend, doctor) ([030d0fb](https://github.com/yorch/ccshelf/commit/030d0fbe6a911d183cbba4d1c888d8e1765e2519))
* add profile manifests, sources, resolution, config and schemas ([28212a2](https://github.com/yorch/ccshelf/commit/28212a2f1d20a2d8d7209d5f73019c603170abe5))
* add the ccshelf root command and main ([c603686](https://github.com/yorch/ccshelf/commit/c60368610e7d424e0ee01d097da74654bdb94575))
* add the claude integration, settings generator, cache and fake claude ([42038cd](https://github.com/yorch/ccshelf/commit/42038cdb5d0b260ed9859b9385001970ed2a11cb))
* add the marketplace reader, org config and catalog ([1dfd8b8](https://github.com/yorch/ccshelf/commit/1dfd8b80442aeb5debfaa29f233529134f8d9f93))
* add the reusable GitHub Action and the starter org data repo ([fb17078](https://github.com/yorch/ccshelf/commit/fb170788c23df6fe2da20cbdb41ffcacbcff8ba7))
* add the trust store, git and plugin profile sources ([f625a13](https://github.com/yorch/ccshelf/commit/f625a137cd5926d250200d6f6737bdd18ed362a4))
* add the UI layer, shell integration and accounts ([b204067](https://github.com/yorch/ccshelf/commit/b2040673491b8a3d32a8815b905a545d01b5cbe4))
* bind plugin sources to their marketplace, warn on deprecated plugins, catalog for git sources ([686b676](https://github.com/yorch/ccshelf/commit/686b676180b25ccb987b8bd2b22c93206e000bfa))
* initialize the Go module and add the env-variable allowlist ([3fd8078](https://github.com/yorch/ccshelf/commit/3fd8078f63f8f7e9b5c8f7007563bf1d3ebc5c28))
* **site:** add the ccshelf website with a manual Pages deploy ([#1](https://github.com/yorch/ccshelf/issues/1)) ([41e681a](https://github.com/yorch/ccshelf/commit/41e681a93a21801327716767031ac2e16cedc949))
* **site:** credit the author in the footer ([#9](https://github.com/yorch/ccshelf/issues/9)) ([ad4c012](https://github.com/yorch/ccshelf/commit/ad4c012a767c6351c6b0f699304af7a825f90a54))
* **site:** publish the Markdown docs as a docs section ([#4](https://github.com/yorch/ccshelf/issues/4)) ([4649c8d](https://github.com/yorch/ccshelf/commit/4649c8d0dd3a1af50acc98ac345074e571f42225))


### Bug fixes

* add --version, verify action pin comments in CI, reproducible make dates ([73216f9](https://github.com/yorch/ccshelf/commit/73216f90ec6a8845face92288d0a1d0c7582b660))
* bind trust to the reviewed hash and read git sources without filters ([eb37687](https://github.com/yorch/ccshelf/commit/eb37687e2d57a2f9aa55e94054280bf961aa3abf))
* close the profile schema against case variants and tighten confinement ([a1ed584](https://github.com/yorch/ccshelf/commit/a1ed584800baef4385dc9f43711ed247cae11bf5))
* confine catalog output, honor usage redaction, check policy conflicts in doctor ([e056423](https://github.com/yorch/ccshelf/commit/e056423d00d4366fa43b421939cf9c6066cef5ee))
* fail closed on plugin-list shapes, protect MCP, harden cache and spawn ([81e066c](https://github.com/yorch/ccshelf/commit/81e066cf721976344070c75a08c2f326d4bb00f2))
* harden CI, release and the Action after adversarial review ([16d4791](https://github.com/yorch/ccshelf/commit/16d4791567c73f295f0a9ace24e1733fd30fff1f))
* harden policy detection and the bundle compiler after review ([ecdf3b1](https://github.com/yorch/ccshelf/commit/ecdf3b1bcd2f7dbeded29f2b25d710b355a5727b))
* keep protect lists enforced when a source fails, fall back to verified cache ([005873d](https://github.com/yorch/ccshelf/commit/005873d68a8422cdb8eecbf9442d352e79adb8b1))
* keep protected plugins enabled without the user layer, deny MCP under strict ([a6da208](https://github.com/yorch/ccshelf/commit/a6da208bc8700ffd5dab79682b300d1f93ff2232))
* keep the cmd shell-init golden file byte-exact (CRLF) ([2b8328a](https://github.com/yorch/ccshelf/commit/2b8328a84199194a7a927e4f2aab68b21fa5f6ca))
* launcher findings from review L, bare picker, ui prefs and JSON errors ([4eb9ee1](https://github.com/yorch/ccshelf/commit/4eb9ee1945cb007debba5701176c08ecc4f0661b))
* layout-versioned checkouts, catalog reads only trusted commits, kind-aware marketplace identity ([3a79c23](https://github.com/yorch/ccshelf/commit/3a79c23a528bb1b04c75faa523ee821e0269c9cc))
* let git explain a failed HEAD verification of a cached checkout ([b5cc693](https://github.com/yorch/ccshelf/commit/b5cc69365d3ed76bd99981b4458ae9271b769604))
* let git for Windows read cached checkouts below long paths ([023e139](https://github.com/yorch/ccshelf/commit/023e1399b687fb2e711740072f352a46140200fe))
* load org config from git sources, isolate failing sources, prune the cache ([56057d6](https://github.com/yorch/ccshelf/commit/56057d62773e341a914ba050df21036a7bc535e4))
* plugin ids must start with a letter or digit in every parser and schema ([7ec0d0e](https://github.com/yorch/ccshelf/commit/7ec0d0ea71d39d378b5f34507e63e62af72b9c06))
* protect plugins that own protected MCP servers, never deny their labels ([7edbac1](https://github.com/yorch/ccshelf/commit/7edbac136e94569d12cec6ce0072935497f1b2c1))
* restore echo on cancel, sanitize prompts, safer quoting and redaction ([9037835](https://github.com/yorch/ccshelf/commit/9037835fef7799b4dd1e02bf8d0e8e87d6e2cbe3))
* route inline hooks and MCP to platform review, fix the starter template ([fee5fa4](https://github.com/yorch/ccshelf/commit/fee5fa4615d67252911c219f338b17e56a2ff79d))
* spell the git null config as /dev/null; more Windows test fixes ([c2a679d](https://github.com/yorch/ccshelf/commit/c2a679d6af7272aa59cff9bb7a7626cb07484adc))
* strict documented-launcher shape for PRF002, meaningful sha256 refusal check, coverage exception deadline ([736c985](https://github.com/yorch/ccshelf/commit/736c985d13712b46d3760d9d6e8af7cba4e418ff))
* verify every tree object and ignore replace refs for cached git sources ([4b5f9b0](https://github.com/yorch/ccshelf/commit/4b5f9b05b29a9427cc1f1bcd6c62237c5b0b5fd9))
