# Regional ADR reference history

The unmerged regional implementation was renumbered on 2026-10-08 before integrating upstream commit 2442c913cffd3790c690403541e56721dfda5936. Regional ADRs 585–662 are now 713–790. The merge base contains no references to the old range; the renumbering applies only to this branch's decisions and citations. Upstream decision numbers remain unchanged.

Append-only migration files retain their historical citations. Follow the decision title and filename in this table to distinguish regional decisions from upstream decisions with the same old number. The earlier 459–498 → 585–624 renumbering remains historical context.

| Previous regional ADR | Current decision |
| --- | --- |
| 585 | [713 · capacity projection jit](713-capacity-projection-jit.md) |
| 586 | [714 · regional build export handoff](714-regional-build-export-handoff.md) |
| 587 | [715 · regional runtime log retention](715-regional-runtime-log-retention.md) |
| 588 | [716 · regional runtime log local collection](716-regional-runtime-log-local-collection.md) |
| 589 | [717 · regional service recovery capacity](717-regional-service-recovery-capacity.md) |
| 590 | [718 · ordinary placement recovery headroom](718-ordinary-placement-recovery-headroom.md) |
| 591 | [719 · regional compute placement](719-regional-compute-placement.md) |
| 592 | [720 · compute region registry](720-compute-region-registry.md) |
| 593 | [721 · regional project deployment](721-regional-project-deployment.md) |
| 594 | [722 · regional managed jobs](722-regional-managed-jobs.md) |
| 595 | [723 · regional deployment previews](723-regional-deployment-previews.md) |
| 596 | [724 · regional runtime artifacts](724-regional-runtime-artifacts.md) |
| 597 | [725 · regional builder claims and cleanup](725-regional-builder-claims-and-cleanup.md) |
| 598 | [726 · regional source references](726-regional-source-references.md) |
| 599 | [727 · regional build sboms](727-regional-build-sboms.md) |
| 600 | [728 · regional runtime log archives](728-regional-runtime-log-archives.md) |
| 601 | [729 · regional control plane cell identity](729-regional-control-plane-cell-identity.md) |
| 602 | [730 · global app directory and regional bindings](730-global-app-directory-and-regional-bindings.md) |
| 603 | [731 · immutable regional deployment targets](731-immutable-regional-deployment-targets.md) |
| 604 | [732 · durable regional image release receipts](732-durable-regional-image-release-receipts.md) |
| 605 | [733 · regional global release generation fences](733-regional-global-release-generation-fences.md) |
| 606 | [734 · global image release journal and request reconciliation](734-global-image-release-journal-and-request-reconciliation.md) |
| 607 | [735 · scoped regional image release delegation](735-scoped-regional-image-release-delegation.md) |
| 608 | [736 · durable regional delegation recovery](736-durable-regional-delegation-recovery.md) |
| 609 | [737 · global directory organization owners](737-global-directory-organization-owners.md) |
| 610 | [738 · regional organization grant consent](738-regional-organization-grant-consent.md) |
| 611 | [739 · regional organization bindings](739-regional-organization-bindings.md) |
| 612 | [740 · global runtime quota reservations](740-global-runtime-quota-reservations.md) |
| 613 | [741 · signed regional runtime quota authority](741-signed-regional-runtime-quota-authority.md) |
| 614 | [742 · durable regional runtime quota journal](742-durable-regional-runtime-quota-journal.md) |
| 615 | [743 · regional runtime admission boundaries](743-regional-runtime-admission-boundaries.md) |
| 616 | [744 · scheduler regional runtime authority](744-scheduler-regional-runtime-authority.md) |
| 617 | [745 · regional runtime quota write fences](745-regional-runtime-quota-write-fences.md) |
| 618 | [746 · regional runtime capacity receipts](746-regional-runtime-capacity-receipts.md) |
| 619 | [747 · regional host incarnations and operation fences](747-regional-host-incarnations-and-operation-fences.md) |
| 620 | [748 · regional host epoch discovery](748-regional-host-epoch-discovery.md) |
| 621 | [749 · regional runtime plan and purpose binding](749-regional-runtime-plan-and-purpose-binding.md) |
| 622 | [750 · issued regional runtime placement checks](750-issued-regional-runtime-placement-checks.md) |
| 623 | [751 · atomic regional launch issuance](751-atomic-regional-launch-issuance.md) |
| 624 | [752 · explicit regional serving resume](752-explicit-regional-serving-resume.md) |
| 625 | [753 · regional image preparation peer recovery](753-regional-image-preparation-peer-recovery.md) |
| 626 | [754 · durable regional serving resume intents](754-durable-regional-serving-resume-intents.md) |
| 627 | [755 · regional warm resume promotion and recovery](755-regional-warm-resume-promotion-and-recovery.md) |
| 628 | [756 · regional migration source and rollback journal](756-regional-migration-source-and-rollback-journal.md) |
| 629 | [757 · native regional runtime provenance](757-native-regional-runtime-provenance.md) |
| 630 | [758 · regional native retirement](758-regional-native-retirement.md) |
| 631 | [759 · authenticated regional native retirement](759-authenticated-regional-native-retirement.md) |
| 632 | [760 · stable regional quota signing keys](760-stable-regional-quota-signing-keys.md) |
| 633 | [761 · retained regional launch sources](761-retained-regional-launch-sources.md) |
| 634 | [762 · original regional launch failure recovery](762-original-regional-launch-failure-recovery.md) |
| 635 | [763 · regional runtime resources and inventory](763-regional-runtime-resources-and-inventory.md) |
| 636 | [764 · regional startup runtime accounting](764-regional-startup-runtime-accounting.md) |
| 637 | [765 · native regional launch resource provenance](765-native-regional-launch-resource-provenance.md) |
| 638 | [766 · regional dispatch reservation resource binding](766-regional-dispatch-reservation-resource-binding.md) |
| 639 | [767 · native producer inventory barrier](767-native-producer-inventory-barrier.md) |
| 640 | [768 · observe native machine before regional resume](768-observe-native-machine-before-regional-resume.md) |
| 641 | [769 · observe native runtime under inventory barrier](769-observe-native-runtime-under-inventory-barrier.md) |
| 642 | [770 · reconcile independent runtime first fences](770-reconcile-independent-runtime-first-fences.md) |
| 643 | [771 · preflight native cold boot dimensions](771-preflight-native-cold-boot-dimensions.md) |
| 644 | [772 · directory quota delivery and regional receiver](772-directory-quota-delivery-and-regional-receiver.md) |
| 645 | [773 · revocable regional quota delivery consent](773-revocable-regional-quota-delivery-consent.md) |
| 646 | [774 · retained directory runtime consent](774-retained-directory-runtime-consent.md) |
| 647 | [775 · durable regional quota delivery](775-durable-regional-quota-delivery.md) |
| 648 | [776 · observe native launch tasks during reconciliation](776-observe-native-launch-tasks-during-reconciliation.md) |
| 649 | [777 · transport native inventory for regional reconciliation](777-transport-native-inventory-for-regional-reconciliation.md) |
| 650 | [778 · snapshot input shape and artifact provenance](778-snapshot-input-shape-and-artifact-provenance.md) |
| 651 | [779 · retain original native executable provenance](779-retain-original-native-executable-provenance.md) |
| 652 | [780 · observe retained original host helper tasks](780-observe-retained-original-host-helper-tasks.md) |
| 653 | [781 · bind app task runtime to original lease](781-bind-app-task-runtime-to-original-lease.md) |
| 654 | [782 · observe native helper families](782-observe-native-helper-families.md) |
| 655 | [783 · retain host helper private sessions](783-retain-host-helper-private-sessions.md) |
| 656 | [784 · cached all selected global ingress](784-cached-all-selected-global-ingress.md) |
| 657 | [785 · explicit regional apps domain](785-explicit-regional-apps-domain.md) |
| 658 | [786 · durable global ingress publication](786-durable-global-ingress-publication.md) |
| 659 | [787 · retain original native launch source](787-retain-original-native-launch-source.md) |
| 660 | [788 · bind original startup row retirement](788-bind-original-startup-row-retirement.md) |
| 661 | [789 · recover startup row charges after original stop](789-recover-startup-row-charges-after-original-stop.md) |
| 662 | [790 · observe current regional runtime attempts](790-observe-current-regional-runtime-attempts.md) |
