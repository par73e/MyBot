"""Ordered cutover state machine. Its driver must fence both schedulers.

All pre-exposure work is reversible using the driver's offline checkpoint.
After exposing the new platform, never restore an old database automatically:
users may already have written new data. The driver must repair forward.
"""


def transition(operations):
    exposed = False
    fenced = False
    try:
        operations.verify_prepared()
        # A fence can fail after stopping only some services.
        fenced = True
        operations.fence_old()
        operations.checkpoint_before()
        operations.snapshot_and_install()
        operations.start_stopped_bridge()
        operations.commit_ownership()
        operations.checkpoint_after()
        operations.validate_private_platform()
        operations.prepare_public_container()
        # Persist before the side effect: an interrupted or partially successful
        # start cannot safely be treated as if no user accessed the new app.
        operations.mark_exposure_pending()
        exposed = True
        operations.expose_public_platform()
        operations.verify_public_platform()
        operations.start_user_assistant()
        operations.enable_provisioner()
        operations.finish()
    except BaseException:
        if fenced and not exposed:
            operations.rollback_before_exposure()
        raise
