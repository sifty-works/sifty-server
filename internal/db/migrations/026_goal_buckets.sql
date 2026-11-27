UPDATE profile
SET    data = json_set(data, '$.workMode', json_object(
           'preferred', json(data -> '$.workModes'),
           CASE WHEN data ->> '$.relocation' = 'yes' THEN 'unwanted' ELSE 'never' END,
           json((SELECT json_group_array(value) FROM json_each('["remote","hybrid","onsite"]')
                 WHERE value NOT IN (SELECT value FROM json_each(profile.data, '$.workModes'))))))
WHERE  json_array_length(data, '$.workModes') > 0;

UPDATE profile
SET    data = json_set(data, '$.engagementType', json_object(
           'preferred', json(data -> '$.engagement'),
           'unwanted', json((SELECT json_group_array(value) FROM json_each('["employment","contract","internship"]')
                             WHERE value NOT IN (SELECT value FROM json_each(profile.data, '$.engagement'))))))
WHERE  json_array_length(data, '$.engagement') > 0;

UPDATE profile
SET    data = json_set(data, '$.workingTime', json_object(
           'preferred', json_array(data ->> '$.hours.mode'),
           'unwanted', json((SELECT json_group_array(value) FROM json_each('["full_time","part_time"]')
                             WHERE value <> profile.data ->> '$.hours.mode'))))
WHERE  coalesce(data ->> '$.hours.mode', '') <> '';

UPDATE profile
SET    data = json_set(data, '$.locations', json_object('preferred', json(data -> '$.preferredLocations')))
WHERE  json_array_length(data, '$.preferredLocations') > 0;

UPDATE profile
SET    data = json_remove(data, '$.workModes', '$.engagement', '$.preferredLocations', '$.hours.mode');

UPDATE profile
SET    data = json_remove(data, '$.hours')
WHERE  data -> '$.hours' = '{}';
