import importlib.util
from pathlib import Path
import pytest

spec=importlib.util.spec_from_file_location('instance_transition',Path(__file__).parents[1]/'deploy/instance_transition.py')
m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)

class Fake:
    def __init__(self, fail=None): self.calls=[];self.fail=fail
    def __getattr__(self,name):
        def call():
            self.calls.append(name)
            if name==self.fail:raise RuntimeError(name)
        return call


def test_transition_orders_fence_snapshot_exposure_and_start():
    f=Fake();m.transition(f)
    assert f.calls == ['verify_prepared','fence_old','checkpoint_before','snapshot_and_install',
                       'start_stopped_bridge','commit_ownership','checkpoint_after','validate_private_platform',
                       'prepare_public_container','mark_exposure_pending','expose_public_platform',
                       'verify_public_platform','start_user_assistant','enable_provisioner','finish']


@pytest.mark.parametrize('failure',['fence_old','checkpoint_before','snapshot_and_install','start_stopped_bridge',
                                     'commit_ownership','checkpoint_after','validate_private_platform',
                                     'prepare_public_container','mark_exposure_pending'])
def test_transition_rolls_back_only_before_possible_public_access(failure):
    f=Fake(failure)
    with pytest.raises(RuntimeError):m.transition(f)
    assert f.calls[-1]=='rollback_before_exposure'
    assert 'expose_public_platform' not in f.calls


@pytest.mark.parametrize('failure',['expose_public_platform','verify_public_platform','start_user_assistant','enable_provisioner'])
def test_transition_never_discards_writes_after_exposure_may_have_started(failure):
    f=Fake(failure)
    with pytest.raises(RuntimeError):m.transition(f)
    assert 'rollback_before_exposure' not in f.calls
