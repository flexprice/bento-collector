import unittest

from simulate import event_ids


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

    def test_replay_normalizes_ids_the_same_way(self):
        events = [{"event_id": "event-1", "id": "storage-row-1"}]
        self.assertEqual(event_ids(events), event_ids(list(events)))


if __name__ == "__main__":
    unittest.main()
