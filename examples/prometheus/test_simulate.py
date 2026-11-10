import unittest

from simulate import event_ids, event_keys


class EventIDsTest(unittest.TestCase):
    def test_event_id_normalization(self):
        for event in [
            {"event_id": "event-1"},
            {"id": "event-1"},
            {"event_id": "event-1", "id": "storage-row-1"},
            {"event_id": None, "id": "event-1"},
        ]:
            with self.subTest(event=event):
                self.assertEqual(event_ids([event]), {"event-1"})

    def test_replay_identity_includes_timestamp(self):
        event = {"event_id": "event-1", "timestamp": "2026-01-01T12:10:00Z"}
        replay = {"id": "event-1", "timestamp": event["timestamp"]}
        self.assertEqual(event_keys([event]), event_keys([replay]))
        replay["timestamp"] = "2026-01-01T12:10:01Z"
        self.assertNotEqual(event_keys([event]), event_keys([replay]))


if __name__ == "__main__":
    unittest.main()
